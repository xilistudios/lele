package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// B1 — per-agent `tools` allowlist (config layer only).
//
// Semantics pinned here (see AgentConfig.Tools doc comment):
//   - nil / absent / JSON null / []  => "all tools" (default behavior)
//   - non-empty                       => allowlist of tool names; unknown
//     names are ignored with a warning at instance-build time (B2, runtime).
//
// These tests guard the whole config path, because the two historical
// field-loss bugs in this package (agents Temperature, agents.defaults
// max_read_lines / subagent_max_iterations) were both silent-drop bugs in
// hand-written copy blocks, not parse bugs. Every path that copies an agent
// must carry Tools:
//
//	file -> LoadConfig (runtime Config)              : struct tag
//	file -> LoadEditableDocument (WebUI doc)         : via LoadConfig
//	doc  -> ToConfig (editable -> runtime)           : hand-written copy  <-- risk
//	cfg  -> editableDocumentFromConfig (runtime->doc): direct struct conv <-- risk
//	doc  -> toSerializable -> disk (WebUI save)      : whole-struct emit

// writeAgentToolsConfig writes a minimal config with the given agents JSON.
func writeAgentToolsConfig(t *testing.T, agentsJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"agents":{"defaults":{"workspace":"/test","provider":"openai","model":"gpt-4o","max_tokens":1024},"list":` + agentsJSON + `}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestAgentConfigToolsRoundTrip is the disk round-trip for the new field:
// LoadConfig -> SaveConfig -> LoadConfig must preserve both a present
// allowlist and the absent (nil) case, and the JSON on disk must not grow a
// "tools" key when the agent has none (omitempty, backward compat).
func TestAgentConfigToolsRoundTrip(t *testing.T) {
	path := writeAgentToolsConfig(t, `[
		{"id":"coder","tools":["read_file","edit_file","exec"]},
		{"id":"chatbot"}
	]`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Agents.List) != 2 {
		t.Fatalf("agents = %d, want 2", len(cfg.Agents.List))
	}

	coder := cfg.Agents.List[0]
	if want := []string{"read_file", "edit_file", "exec"}; !stringSlicesEqualForTest(coder.Tools, want) {
		t.Errorf("coder tools = %v, want %v", coder.Tools, want)
	}
	chatbot := cfg.Agents.List[1]
	if chatbot.Tools != nil {
		t.Errorf("chatbot tools = %v, want nil (absent = all tools)", chatbot.Tools)
	}

	// Re-save through the runtime path and re-load: values must survive.
	out := filepath.Join(t.TempDir(), "saved.json")
	if err := SaveConfig(out, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	var onDisk map[string]interface{}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal saved: %v", err)
	}
	list := onDisk["agents"].(map[string]interface{})["list"].([]interface{})
	if _, ok := list[1].(map[string]interface{})["tools"]; ok {
		t.Errorf("agent without tools wrote a \"tools\" key; omitempty must keep old configs byte-compatible:\n%s", raw)
	}

	reloaded, err := LoadConfig(out)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if !stringSlicesEqualForTest(reloaded.Agents.List[0].Tools, coder.Tools) {
		t.Errorf("after re-save tools = %v, want %v", reloaded.Agents.List[0].Tools, coder.Tools)
	}
	if reloaded.Agents.List[1].Tools != nil {
		t.Errorf("after re-save chatbot tools = %v, want nil", reloaded.Agents.List[1].Tools)
	}
}

// TestAgentConfigToolsEmptyAndNullStayInherit pins the edge cases of the
// "nil/empty = all tools" contract: absent, JSON null and [] all load to a
// zero-length slice, which downstream code (B2 runtime filter) must treat as
// "no restriction" — never as "no tools allowed". JSON null and absent yield
// nil; [] yields an empty non-nil slice (standard encoding/json behavior,
// identical to skills), so consumers MUST branch on len(Tools)==0 and not on
// nil-ness. Asserted explicitly so that rule cannot regress silently.
func TestAgentConfigToolsEmptyAndNullStayInherit(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantN   int
		wantNil bool
	}{
		{"absent", `[{"id":"a"}]`, 0, true},
		{"null", `[{"id":"a","tools":null}]`, 0, true},
		{"empty array", `[{"id":"a","tools":[]}]`, 0, false},
		{"one", `[{"id":"a","tools":["exec"]}]`, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeAgentToolsConfig(t, tc.json))
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			got := cfg.Agents.List[0].Tools
			if len(got) != tc.wantN {
				t.Fatalf("tools = %v (len %d), want len %d", got, len(got), tc.wantN)
			}
			if (got == nil) != tc.wantNil {
				t.Errorf("tools nil-ness = %v, want %v (%#v)", got == nil, tc.wantNil, got)
			}
		})
	}
}

// TestEditableDocument_AgentToolsToConfig guards the hand-written
// editable->runtime copy in ToConfig (the exact block that used to drop
// Temperature): a tools allowlist edited in the WebUI must reach Config.
func TestEditableDocument_AgentToolsToConfig(t *testing.T) {
	doc := &EditableDocument{}
	doc.Agents.Defaults.Workspace = "/test"
	doc.Agents.Defaults.Provider = "openai"
	doc.Agents.Defaults.Model = "gpt-4o"
	doc.Agents.Defaults.MaxTokens = 1024
	doc.Agents.List = []EditableAgentConfig{
		{ID: "coder", Tools: []string{"read_file", "exec"}},
		{ID: "chatbot"},
	}

	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if len(cfg.Agents.List) != 2 {
		t.Fatalf("agents = %d, want 2", len(cfg.Agents.List))
	}
	if want := []string{"read_file", "exec"}; !stringSlicesEqualForTest(cfg.Agents.List[0].Tools, want) {
		t.Errorf("ToConfig dropped tools: got %v, want %v", cfg.Agents.List[0].Tools, want)
	}
	if cfg.Agents.List[1].Tools != nil {
		t.Errorf("agent without tools got %v, want nil", cfg.Agents.List[1].Tools)
	}
}

// TestEditableDocument_AgentToolsFromConfig guards the other direction:
// editableDocumentFromConfig uses a direct struct conversion, so a field
// added to only one of the two structs breaks at compile time — this test
// documents that the conversion also carries the values at runtime.
func TestEditableDocument_AgentToolsFromConfig(t *testing.T) {
	cfg := &Config{}
	cfg.Agents.List = []AgentConfig{{ID: "coder", Tools: []string{"spawn", "exec"}}}

	doc := editableDocumentFromConfig(cfg)
	if len(doc.Agents.List) != 1 {
		t.Fatalf("doc agents = %d, want 1", len(doc.Agents.List))
	}
	if want := []string{"spawn", "exec"}; !stringSlicesEqualForTest(doc.Agents.List[0].Tools, want) {
		t.Errorf("editableDocumentFromConfig tools = %v, want %v", doc.Agents.List[0].Tools, want)
	}
}

// TestEditableDocument_AgentToolsSaveLoadRoundTrip covers the full WebUI
// disk path: SaveEditableDocument -> LoadEditableDocument (GET /api/v1/config
// after a PUT), including toSerializable, which is the map actually written.
func TestEditableDocument_AgentToolsSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	doc := &EditableDocument{}
	doc.Agents.Defaults.Workspace = "/test"
	doc.Agents.Defaults.Provider = "openai"
	doc.Agents.Defaults.Model = "gpt-4o"
	doc.Agents.Defaults.MaxTokens = 1024
	doc.Agents.List = []EditableAgentConfig{
		{ID: "coder", Tools: []string{"read_file", "edit_file"}},
		{ID: "chatbot", Tools: []string{}},
	}

	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	// The on-disk document must carry the allowlist for the agent that has one.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal saved doc: %v", err)
	}
	list, ok := parsed["agents"].(map[string]interface{})["list"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("agents.list missing/wrong size on disk: %v", parsed["agents"])
	}
	coder := list[0].(map[string]interface{})
	got, ok := coder["tools"].([]interface{})
	if !ok || len(got) != 2 || got[0] != "read_file" || got[1] != "edit_file" {
		t.Errorf("on-disk coder tools = %v, want [read_file edit_file]", coder["tools"])
	}
	// Empty allowlist is omitempty: absent on disk, and absent == all tools,
	// so no information is lost (the editor's "all" toggle maps to both).
	if _, ok := list[1].(map[string]interface{})["tools"]; ok {
		t.Errorf("on-disk chatbot has a \"tools\" key, want it omitted (empty = all)")
	}

	reloaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	if len(reloaded.Agents.List) != 2 {
		t.Fatalf("reloaded agents = %d, want 2", len(reloaded.Agents.List))
	}
	if want := []string{"read_file", "edit_file"}; !stringSlicesEqualForTest(reloaded.Agents.List[0].Tools, want) {
		t.Errorf("reloaded coder tools = %v, want %v", reloaded.Agents.List[0].Tools, want)
	}
	if reloaded.Agents.List[1].Tools != nil {
		t.Errorf("reloaded chatbot tools = %v, want nil", reloaded.Agents.List[1].Tools)
	}
}

// TestValidateEditableDocument_AgentToolsAccepted asserts that unknown tool
// names are NOT a validation error: the spec says they are ignored with a
// warning at build time (the config layer cannot know the full registry,
// which lives in pkg/tools). A malformed-shape file, on the other hand, must
// still fail to load rather than silently wipe the allowlist.
func TestValidateEditableDocument_AgentToolsAccepted(t *testing.T) {
	doc := &EditableDocument{}
	doc.Agents.Defaults.Workspace = "/test"
	doc.Agents.Defaults.Provider = "openai"
	doc.Agents.Defaults.Model = "gpt-4o"
	doc.Agents.Defaults.MaxTokens = 1024
	doc.Agents.List = []EditableAgentConfig{
		{ID: "coder", Tools: []string{"read_file", "tool_that_does_not_exist"}},
	}

	if errs := ValidateEditableDocument(doc); len(errs) > 0 {
		t.Errorf("ValidateEditableDocument = %+v, want no errors for unknown tool names", errs)
	}
	if _, err := doc.ToConfig(); err != nil {
		t.Errorf("ToConfig with unknown tool name: %v", err)
	}
}

func TestAgentConfigToolsShapeError(t *testing.T) {
	// tools must be an array of strings; a scalar is a hard load error so a
	// typo cannot silently fall back to "all tools".
	_, err := LoadConfig(writeAgentToolsConfig(t, `[{"id":"coder","tools":"exec"}]`))
	if err == nil {
		t.Fatal("LoadConfig accepted tools as a string, want error")
	}
}

// stringSlicesEqualForTest compares two string slices element-wise.
// Deliberately local: pkg/config has no exported slice helper and the
// runtime one lives in pkg/agent.
func stringSlicesEqualForTest(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
