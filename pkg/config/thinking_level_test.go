package config

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizeThinkingLevel pins the canonicalization contract for
// agents(.defaults).thinking_level: sentinels mean "inherit" ("" + ok),
// the whitelist passes through lowercased/trimmed, anything else is invalid.
func TestNormalizeThinkingLevel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		// Sentinels: valid, mean "no explicit level, inherit".
		{"empty", "", "", true},
		{"default", "default", "", true},
		{"Default mixed case", "Default", "", true},
		{"none", "none", "", true},
		{"NONE upper", "NONE", "", true},
		{"spaces only", "   ", "", true},
		{"default with padding", "  default  ", "", true},
		{"none with padding", "\tnone\n", "", true},
		// Whitelist: valid, canonical lowercase output.
		{"off", "off", "off", true},
		{"OFF", "OFF", "off", true},
		{"  Off  ", "  Off ", "off", true},
		{"low", "low", "low", true},
		{"LOW", "LOW", "low", true},
		{"low padded", " low\t", "low", true},
		{"medium", "medium", "medium", true},
		{"Medium", "Medium", "medium", true},
		{"high", "high", "high", true},
		{"HIGH padded", "  HIGH  ", "high", true},
		// Invalid: ok=false and empty output.
		{"ultra", "ultra", "", false},
		{"minimal", "minimal", "", false},
		{"max", "max", "", false},
		{"auto", "auto", "", false},
		{"on", "on", "", false},
		{"true", "true", "", false},
		{"hi", "hi", "", false},
		{"off with suffix", "off-line", "", false},
		{"double space inside", "me dium", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizeThinkingLevel(tt.in)
			if ok != tt.ok {
				t.Errorf("NormalizeThinkingLevel(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("NormalizeThinkingLevel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestReasoningConfigValidateUnchangedByThinkingLevel documents that the
// per-MODEL reasoning validation keeps its own semantics: "off" is NOT a
// valid model reasoning effort even though it is a valid thinking_level.
func TestReasoningConfigValidateUnchangedByThinkingLevel(t *testing.T) {
	off := "off"
	r := &ReasoningConfig{Effort: &off}
	if err := r.Validate(); err == nil {
		t.Error("ReasoningConfig.Validate() accepted \"off\"; per-model semantics must stay unchanged")
	}
	high := "high"
	r = &ReasoningConfig{Effort: &high}
	if err := r.Validate(); err != nil {
		t.Errorf("ReasoningConfig.Validate() rejected \"high\": %v", err)
	}
}

// TestEditableDocument_ThinkingLevelRoundTrip pins the WebUI save path for
// agents.defaults.thinking_level and agents.list[i].thinking_level (review
// R1): a value set anywhere in the editor must survive
// editableDocumentFromConfig -> ToConfig AND must appear in toSerializable
// (the map actually written to disk). Without the second assertion,
// agents.defaults.thinking_level silently evaporates on gateway restart.
func TestEditableDocument_ThinkingLevelRoundTrip(t *testing.T) {
	high := "high"
	low := "low"
	agentTemp := 0.3

	cfg := &Config{}
	cfg.Agents.Defaults.ThinkingLevel = &high
	// Non-zero values for the two fields the old hand-written map dropped:
	// they carry omitempty, so a zero value would legitimately be absent.
	cfg.Agents.Defaults.MaxReadLines = 500
	cfg.Agents.Defaults.SubagentMaxIterations = 40
	cfg.Agents.List = []AgentConfig{
		{ID: "coder", ThinkingLevel: &low, Temperature: &agentTemp},
	}

	doc := editableDocumentFromConfig(cfg)

	// editableDocumentFromConfig must carry the values into the document.
	if doc.Agents.Defaults.ThinkingLevel == nil || *doc.Agents.Defaults.ThinkingLevel != "high" {
		t.Fatalf("doc defaults thinking_level = %v, want \"high\"", doc.Agents.Defaults.ThinkingLevel)
	}
	if len(doc.Agents.List) != 1 {
		t.Fatalf("doc agents list len = %d, want 1", len(doc.Agents.List))
	}
	if doc.Agents.List[0].ThinkingLevel == nil || *doc.Agents.List[0].ThinkingLevel != "low" {
		t.Fatalf("doc agent thinking_level = %v, want \"low\"", doc.Agents.List[0].ThinkingLevel)
	}

	// ToConfig must copy both back out.
	got, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if got.Agents.Defaults.ThinkingLevel == nil || *got.Agents.Defaults.ThinkingLevel != "high" {
		t.Errorf("ToConfig defaults thinking_level = %v, want \"high\"", got.Agents.Defaults.ThinkingLevel)
	}
	if len(got.Agents.List) != 1 {
		t.Fatalf("ToConfig agents list len = %d, want 1", len(got.Agents.List))
	}
	if got.Agents.List[0].ThinkingLevel == nil || *got.Agents.List[0].ThinkingLevel != "low" {
		t.Errorf("ToConfig agent thinking_level = %v, want \"low\"", got.Agents.List[0].ThinkingLevel)
	}
	// Side-fix: Temperature used to be dropped by the ToConfig agents loop.
	if got.Agents.List[0].Temperature == nil || *got.Agents.List[0].Temperature != 0.3 {
		t.Errorf("ToConfig agent temperature = %v, want 0.3 (preexisting drop must stay fixed)", got.Agents.List[0].Temperature)
	}

	// toSerializable (the map actually written to disk) must carry the fields.
	// Serialize through JSON first: defaults and list are now emitted as whole
	// structs, so the on-disk shape is what matters.
	raw, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal serializable: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal serializable: %v", err)
	}
	agents, ok := parsed["agents"].(map[string]interface{})
	if !ok {
		t.Fatal("agents section missing from serializable doc")
	}
	defaults, ok := agents["defaults"].(map[string]interface{})
	if !ok {
		t.Fatal("agents.defaults missing from serializable doc")
	}
	if defaults["thinking_level"] != "high" {
		t.Errorf("serialized defaults thinking_level = %v, want \"high\"", defaults["thinking_level"])
	}
	// Regression guard for the structural fix: these two fields were silently
	// dropped by the old hand-written defaults map on every WebUI save.
	if _, ok := defaults["max_read_lines"]; !ok {
		t.Error("max_read_lines missing from serialized agents.defaults (structural fix regressed)")
	}
	if _, ok := defaults["subagent_max_iterations"]; !ok {
		t.Error("subagent_max_iterations missing from serialized agents.defaults (structural fix regressed)")
	}
	list, ok := agents["list"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("agents.list missing or wrong size in serializable doc: %v", agents["list"])
	}
	entry, ok := list[0].(map[string]interface{})
	if !ok {
		t.Fatalf("agents.list[0] not an object: %T", list[0])
	}
	if entry["thinking_level"] != "low" {
		t.Errorf("serialized agent thinking_level = %v, want \"low\"", entry["thinking_level"])
	}
	if tv, ok := entry["temperature"].(float64); !ok || tv != 0.3 {
		t.Errorf("serialized agent temperature = %v, want 0.3", entry["temperature"])
	}
}

// TestEditableDocument_ThinkingLevelNilStaysAbsent asserts the tri-state:
// an unset thinking_level must not be materialized as "" or a code default
// anywhere in the save path (nil = inherit).
func TestEditableDocument_ThinkingLevelNilStaysAbsent(t *testing.T) {
	doc := editableDocumentFromConfig(&Config{})
	if doc.Agents.Defaults.ThinkingLevel != nil {
		t.Fatalf("defaults thinking_level = %v, want nil", *doc.Agents.Defaults.ThinkingLevel)
	}
	doc = applyDefaults(doc)
	if doc.Agents.Defaults.ThinkingLevel != nil {
		t.Errorf("applyDefaults invented a thinking_level: %q", *doc.Agents.Defaults.ThinkingLevel)
	}

	raw, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal serializable: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal serializable: %v", err)
	}
	defaults := parsed["agents"].(map[string]interface{})["defaults"].(map[string]interface{})
	if _, ok := defaults["thinking_level"]; ok {
		t.Errorf("thinking_level key present with nil value: %v", defaults["thinking_level"])
	}
}

// TestEditableDocument_ThinkingLevelSaveLoadRoundTrip covers the full disk
// path used by the WebUI: SaveEditableDocument -> LoadEditableDocument.
func TestEditableDocument_ThinkingLevelSaveLoadRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	high := "high"
	low := "low"
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "/test",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
				ThinkingLevel:     &high,
			},
			List: []EditableAgentConfig{
				{ID: "coder", ThinkingLevel: &low},
			},
		},
	}

	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	loaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	if loaded.Agents.Defaults.ThinkingLevel == nil || *loaded.Agents.Defaults.ThinkingLevel != "high" {
		t.Errorf("loaded defaults thinking_level = %v, want \"high\"", loaded.Agents.Defaults.ThinkingLevel)
	}
	if len(loaded.Agents.List) != 1 {
		t.Fatalf("loaded agents list len = %d, want 1", len(loaded.Agents.List))
	}
	if loaded.Agents.List[0].ThinkingLevel == nil || *loaded.Agents.List[0].ThinkingLevel != "low" {
		t.Errorf("loaded agent thinking_level = %v, want \"low\"", loaded.Agents.List[0].ThinkingLevel)
	}

	// Runtime path: LoadConfig (used by the gateway reload) must see it too.
	runtime, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if runtime.Agents.Defaults.ThinkingLevel == nil || *runtime.Agents.Defaults.ThinkingLevel != "high" {
		t.Errorf("runtime defaults thinking_level = %v, want \"high\"", runtime.Agents.Defaults.ThinkingLevel)
	}
	if len(runtime.Agents.List) != 1 || runtime.Agents.List[0].ThinkingLevel == nil || *runtime.Agents.List[0].ThinkingLevel != "low" {
		t.Errorf("runtime agent thinking_level not persisted: %+v", runtime.Agents.List)
	}
}

// TestValidateEditableDocument_ThinkingLevel covers the whitelist: invalid
// values produce one issue with the right Path for defaults and for each
// list entry; inherit sentinels and "off" are accepted.
func TestValidateEditableDocument_ThinkingLevel(t *testing.T) {
	ptr := func(s string) *string { return &s }

	// findIssue returns the issue for path, or nil.
	findIssue := func(issues []ValidationError, path string) *ValidationError {
		for i := range issues {
			if issues[i].Path == path {
				return &issues[i]
			}
		}
		return nil
	}

	baseDoc := func() *EditableDocument {
		return &EditableDocument{
			Agents: EditableAgentsConfig{
				Defaults: EditableAgentDefaults{
					Workspace:         "/test",
					Provider:          "openai",
					Model:             "gpt-4",
					MaxTokens:         8192,
					MaxToolIterations: 20,
				},
			},
		}
	}

	tests := []struct {
		name        string
		defaults    *string
		agentLevels []*string
		wantErrAt   []string // paths that must be reported
		wantOKAt    []string // paths that must NOT be reported
	}{
		{
			name:     "invalid ultra in defaults",
			defaults: ptr("ultra"),
			wantErrAt: []string{
				"agents.defaults.thinking_level",
			},
		},
		{
			name:        "invalid ultra in list entry",
			agentLevels: []*string{ptr("ultra")},
			wantErrAt: []string{
				"agents.list.0.thinking_level",
			},
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
		{
			name:        "invalid in second list entry only",
			agentLevels: []*string{ptr("high"), ptr("turbo")},
			wantErrAt: []string{
				"agents.list.1.thinking_level",
			},
			wantOKAt: []string{"agents.list.0.thinking_level", "agents.defaults.thinking_level"},
		},
		{
			name:     "off accepted in defaults",
			defaults: ptr("off"),
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
		{
			name:     "empty string accepted (inherit)",
			defaults: ptr(""),
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
		{
			name:     "default sentinel accepted",
			defaults: ptr("default"),
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
		{
			name:     "none sentinel accepted",
			defaults: ptr("none"),
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
		{
			name:     "case and padding accepted",
			defaults: ptr("  HIGH "),
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
		{
			name:     "nil inherits without error",
			defaults: nil,
			wantOKAt: []string{"agents.defaults.thinking_level"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := baseDoc()
			doc.Agents.Defaults.ThinkingLevel = tt.defaults
			for i, lvl := range tt.agentLevels {
				doc.Agents.List = append(doc.Agents.List, EditableAgentConfig{
					ID:            fmt.Sprintf("agent-%d", i),
					ThinkingLevel: lvl,
				})
			}

			issues := ValidateEditableDocument(doc)
			for _, p := range tt.wantErrAt {
				issue := findIssue(issues, p)
				if issue == nil {
					t.Errorf("expected validation issue at %q, got %v", p, issues)
					continue
				}
				if issue.Code != "invalid_enum" {
					t.Errorf("issue at %q code = %q, want invalid_enum", p, issue.Code)
				}
				if !strings.Contains(issue.Message, "invalid thinking_level") {
					t.Errorf("issue at %q message = %q, want invalid thinking_level wording", p, issue.Message)
				}
			}
			for _, p := range tt.wantOKAt {
				if issue := findIssue(issues, p); issue != nil {
					t.Errorf("unexpected validation issue at %q: %s", p, issue.Message)
				}
			}
		})
	}
}
