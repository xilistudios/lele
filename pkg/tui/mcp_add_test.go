package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func mcpAddPaths(t *testing.T) mcp.Paths {
	t.Helper()
	m := newTestModel(t)
	if _, ok := m.mcpPaths(); !ok {
		t.Fatal("m.mcpPaths() = false, the fixture must resolve the MCP roots")
	}
	root := t.TempDir()
	return mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
}

func mcpAddEntry() mcp.ServerConfig {
	return mcp.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
}

func mcpAddOpenList(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.width, m.height = 140, 40
	m.executeCommand("/mcp")
	if m.modalMode != ModalMCP {
		t.Fatalf("mode = %v, want ModalMCP", m.modalMode)
	}
	return m
}

func mcpAddSwapSyncCounter(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := syncMCPServersFor
	syncMCPServersFor = func(al *agent.AgentLoop) { calls++ }
	t.Cleanup(func() { syncMCPServersFor = orig })
	return &calls
}

func TestMCPAddResult_RoutedByUpdateWithoutLeakingIntoInput(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.chatInput.SetValue("typed")
	if m.modalMode != ModalNone {
		t.Fatalf("precondition: modalMode = %v, want ModalNone", m.modalMode)
	}

	_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", err: errors.New("boom")})

	if !strings.Contains(m.mcpFeedback, "svc") {
		t.Errorf("add-result not routed to handler: mcpFeedback = %q", m.mcpFeedback)
	}
	if got := m.chatInput.Value(); got != "typed" {
		t.Errorf("text input mutated by the msg: %q", got)
	}
}

func TestMCPAddResult_TextareaSkipListEntry(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed: cannot locate this test's own file")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "handlers.go"))
	if err != nil {
		t.Fatalf("read handlers.go: %v", err)
	}
	text := string(src)
	start := strings.Index(text, "case outboundMsg, completeMsg, tickMsg,")
	if start < 0 {
		t.Fatal("handlers.go: the finishUpdate textarea skip case was not found")
	}
	end := strings.Index(text[start:], "\n")
	if end < 0 {
		t.Fatal("handlers.go: the finishUpdate textarea skip case is not a single line")
	}
	clause := text[start : start+end]
	if !strings.Contains(clause, "mcpAddResultMsg") {
		t.Errorf("the finishUpdate textarea skip list does not include mcpAddResultMsg:\n%s\n"+
			"(a result msg falling through reaches the chat textarea's Update; harmless today "+
			"but the sibling list is the documented contract)", clause)
	}
	for _, needle := range []struct{ what, want string }{
		{"the update() dispatch list", "mcpToggleResultMsg, mcpAddResultMsg, mcpAuthStartMsg"},
		{"the handleAsyncResult case", "case mcpAddResultMsg:"},
	} {
		if !strings.Contains(text, needle.want) && needle.what == "the update() dispatch list" {
			t.Errorf("handlers.go: %s does not carry mcpAddResultMsg next to its siblings", needle.what)
		}
		if needle.what == "the handleAsyncResult case" {
			asyncSrc, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "handlers_async.go"))
			if err != nil {
				t.Fatalf("read handlers_async.go: %v", err)
			}
			if !strings.Contains(string(asyncSrc), needle.want) {
				t.Errorf("handlers_async.go: the handleAsyncResult switch has no %s", needle.want)
			}
		}
	}
}

func TestMCPAddErrorKind_Table(t *testing.T) {
	const secret = "s3cr3t-value"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"shaped write", errors.New("mcp: write /home/u/.lele/mcp.json: bad value \"" + secret + "\""), "write"},
		{"shaped read", errors.New("mcp: read /home/u/.lele/mcp.json: permission denied"), "read"},
		{"shaped edit", errors.New("mcp: edit /home/u/.lele/mcp.json: truncated"), "edit"},
		{"shaped replace", errors.New("mcp: replace /tmp/x: no space left on device"), "replace"},
		{"unshaped", errors.New("permission denied: /home/u/.lele/mcp.json"), "permission"},
		{"control chars stripped", errors.New("some\x1b[31mstrict\x07thing"), "somestrictthing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpAddErrorKind(tc.err)
			if got != tc.want {
				t.Errorf("mcpAddErrorKind(%v) = %q, want %q", tc.err, got, tc.want)
			}
			if strings.Contains(got, secret) {
				t.Errorf("mcpAddErrorKind(%v) = %q leaks the value %q", tc.err, got, secret)
			}
		})
	}
}

func TestMCPAddResult_FeedbackNeverEchoesRawError(t *testing.T) {
	crafty := errors.New("mcp: write /home/u/.lele/mcp.json: bad value \"TOKEN=" + "s3cr3t" + "\"\x1b[31m\x07")

	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", err: crafty})

	fb := m.mcpFeedback
	if fb == "" {
		t.Fatal("feedback line is empty: the error was not surfaced at all")
	}
	if strings.Contains(fb, "s3cr3t") {
		t.Errorf("feedback leaks the value: %q", fb)
	}
	if strings.Contains(fb, "mcp.json") {
		t.Errorf("feedback leaks the file path: %q", fb)
	}
	if strings.Contains(fb, "=") {
		t.Errorf("feedback carries a KEY=VALUE fragment (want verb only): %q", fb)
	}
	if strings.ContainsAny(fb, "\x1b\x07") {
		t.Errorf("feedback carries control characters: %q", fb)
	}
	if want := fmt.Sprintf(i18n.T("tui.mcpAddFailed"), "svc", "write"); fb != want {
		t.Errorf("feedback = %q, want the verb-only rendering %q", fb, want)
	}
}

func TestMCPAddCmdFor_WritesOnlyTheNamedLayer(t *testing.T) {
	paths := mcpFixturePaths(t)
	_ = mcpAddOpenList(t)
	globalFile := filepath.Join(paths.LeleDir, "mcp.json")
	agentFile := filepath.Join(paths.AgentWorkspace, "mcp.json")
	projectFile := filepath.Join(paths.Cwd, ".lele", "mcp.json")

	before := map[string][]byte{}
	for _, f := range []string{globalFile, agentFile, projectFile} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		before[f] = b
	}

	got := mcpAddCmdFor(paths, mcp.LayerAgent, "newsvc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("add failed: %v", res.err)
	}
	if !res.created || !res.changed {
		t.Errorf("created=%v changed=%v, want both true", res.created, res.changed)
	}
	if res.layer != mcp.LayerAgent {
		t.Errorf("layer = %q, want %q", res.layer, mcp.LayerAgent)
	}
	if res.path != agentFile {
		t.Errorf("path = %q, want %q", res.path, agentFile)
	}

	agentAfter, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	if !bytes.Contains(agentAfter, []byte(`"newsvc"`)) {
		t.Errorf("agent file lacks the new entry: %s", agentAfter)
	}

	for _, f := range []string{globalFile, projectFile} {
		after, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("re-read %s: %v", f, err)
		}
		if !bytes.Equal(before[f], after) {
			t.Errorf("%s modified:\nbefore=%s\nafter=%s", f, before[f], after)
		}
	}
}

func TestMCPAddCmdFor_RefusesDisabledLayer(t *testing.T) {
	paths := mcpAddPaths(t)
	paths.LeleDir = ""
	if f := mcp.LayerFile(paths, mcp.LayerGlobal); f != "" {
		t.Fatalf("fixture is wrong: LayerFile(global) = %q, want \"\" (disabled layer)", f)
	}

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "svc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if !errors.Is(res.err, errMCPOwnerNoPath) {
		t.Errorf("err = %v, want errMCPOwnerNoPath", res.err)
	}
	if res.layer != mcp.LayerGlobal {
		t.Errorf("layer = %q, want %q", res.layer, mcp.LayerGlobal)
	}
	for _, dir := range []string{paths.AgentWorkspace, paths.Cwd} {
		f := filepath.Join(dir, "mcp.json")
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("refused add created %s (stat err = %v)", f, err)
		}
	}
}

func TestMCPAddCmdFor_RefusesTargetOutsideAllowedRoots(t *testing.T) {
	probe, err := os.MkdirTemp("/var/tmp", "lele-tui-guard-*")
	if err != nil {
		t.Fatalf("cannot create a probe dir outside the allowed roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(probe) })

	if channels.IsAllowedWorkspacePath(probe) {
		wd, _ := os.Getwd()
		home, _ := os.UserHomeDir()
		t.Fatalf("probe dir %q is accepted by the predicate (home=%q cwd=%q); the refusal cannot be exercised on this box",
			probe, home, wd)
	}

	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        probe,
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"svc":{"command":"x"}}}`)
	file := filepath.Join(paths.LeleDir, "mcp.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "newsvc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if !errors.Is(res.err, errMCPPathNotAllowed) {
		t.Errorf("err = %v, want errMCPPathNotAllowed", res.err)
	}
	if res.path != file {
		t.Errorf("path = %q, want the refused target %q", res.path, file)
	}

	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("refused target was written:\nbefore=%s\nafter=%s", before, after)
	}

	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(res)
	want := fmt.Sprintf(i18n.T("tui.mcpAddPathNotAllowed"), "newsvc", file)
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
}

func TestMCPAddCmdFor_RefusesInvalidEntry(t *testing.T) {
	paths := mcpAddPaths(t)
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"svc":{"command":"x"}}}`)
	file := filepath.Join(paths.LeleDir, "mcp.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	invalid := mcp.ServerConfig{Command: "npx", URL: "https://example.com/mcp"}
	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "newsvc", invalid)()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err == nil {
		t.Fatal("err = nil, want a validation error for the mixed stdio/remote entry")
	}
	if res.created || res.changed {
		t.Errorf("created=%v changed=%v, want both false on refusal", res.created, res.changed)
	}

	if _, err := os.Stat(filepath.Join(paths.AgentWorkspace, "mcp.json")); !os.IsNotExist(err) {
		t.Errorf("invalid add created an agent file (stat err = %v)", err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("fixture was written on a refused entry:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestMCPAddResult_SyncsOnceOnSuccessAndZeroOnRefusal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		calls := mcpAddSwapSyncCounter(t)
		m := &Model{chatInput: textarea.New()}
		_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", created: true, changed: true})
		if *calls != 1 {
			t.Errorf("sync calls = %d, want exactly 1", *calls)
		}
	})
	t.Run("refusal", func(t *testing.T) {
		calls := mcpAddSwapSyncCounter(t)
		m := &Model{chatInput: textarea.New()}
		_, _ = m.Update(mcpAddResultMsg{name: "svc", err: errMCPUnavailable})
		if *calls != 0 {
			t.Errorf("sync calls = %d, want 0 on refusal", *calls)
		}
	})
	t.Run("no-change success still syncs", func(t *testing.T) {
		calls := mcpAddSwapSyncCounter(t)
		m := &Model{chatInput: textarea.New()}
		_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", changed: false})
		if *calls != 1 {
			t.Errorf("sync calls = %d, want 1 (idempotent pass, like the toggle)", *calls)
		}
	})
}

func TestMCPAddCmdFor_WritesExactBytesAndLiteralToken(t *testing.T) {
	t.Run("exact bytes", func(t *testing.T) {
		paths := mcpAddPaths(t)
		got := mcpAddCmdFor(paths, mcp.LayerGlobal, "x", mcpAddEntry())()
		res, ok := got.(mcpAddResultMsg)
		if !ok {
			t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
		}
		if res.err != nil {
			t.Fatalf("add failed: %v", res.err)
		}
		want := `{"mcpServers":{"x":{"command":"npx","args":["-y","pkg"]}}}`
		after, err := os.ReadFile(filepath.Join(paths.LeleDir, "mcp.json"))
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if string(after) != want {
			t.Errorf("file bytes = %s, want %s", after, want)
		}
	})

	t.Run("literal ${TOKEN} is never expanded", func(t *testing.T) {
		paths := mcpAddPaths(t)
		entry := mcp.ServerConfig{
			URL:     "https://example.com/mcp",
			Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"},
			Env:     map[string]string{"SECRET": "${TOKEN}"},
		}
		got := mcpAddCmdFor(paths, mcp.LayerGlobal, "tok", entry)()
		res, ok := got.(mcpAddResultMsg)
		if !ok {
			t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
		}
		if res.err != nil {
			t.Fatalf("add failed: %v", res.err)
		}
		after, err := os.ReadFile(filepath.Join(paths.LeleDir, "mcp.json"))
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if !bytes.Contains(after, []byte("${TOKEN}")) {
			t.Errorf("${TOKEN} was expanded (or lost) on disk: %s", after)
		}
		if bytes.Contains(after, []byte("Bearer ey")) {
			t.Errorf("a secret leaked into the file: %s", after)
		}
	})
}

func TestMCPAddResult_ResetsFilterThatWouldHideTheNewRow(t *testing.T) {
	m := mcpAddOpenList(t)
	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("fixture lost MCP paths")
	}
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"alpha":{"command":"a"}}}`)

	t.Run("mismatched filter resets and reselects", func(t *testing.T) {
		m.mcpFilter = mcpFilterAll
		m.loadMCPList()
		m.mcpFilter = mcpFilterProject
		m.modalSelectedIdx = 0

		mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"alpha":{"command":"a"},"delta":{"command":"d"}}}`)
		_, _ = m.Update(mcpAddResultMsg{name: "delta", layer: mcp.LayerGlobal, created: true, changed: true})

		if m.mcpFilter != mcpFilterAll {
			t.Errorf("filter = %d, want mcpFilterAll (%d)", m.mcpFilter, mcpFilterAll)
		}
		idx := -1
		for i, k := range m.mcpModalKeys {
			if k == "delta" {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("keys = %v, want delta in the reloaded list", m.mcpModalKeys)
		}
		if m.modalSelectedIdx != idx {
			t.Errorf("cursor = %d, want %d (the new row)", m.modalSelectedIdx, idx)
		}
	})

	t.Run("matching filter is left alone", func(t *testing.T) {
		m.mcpFilter = mcpFilterGlobal
		m.loadMCPList()
		m.modalSelectedIdx = 0

		_, _ = m.Update(mcpAddResultMsg{name: "alpha", layer: mcp.LayerGlobal, created: true, changed: true})

		if m.mcpFilter != mcpFilterGlobal {
			t.Errorf("filter = %d, want mcpFilterGlobal (%d) untouched", m.mcpFilter, mcpFilterGlobal)
		}
	})
}

func TestMCPAddResult_NeverTouchesWizardState(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.formValues = []string{"sentinel-name", "sentinel-kind"}
	m.formStepIndex = 3
	m.formError = "sentinel-error"

	_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", created: true, changed: true})

	if len(m.formValues) != 2 || m.formValues[0] != "sentinel-name" || m.formValues[1] != "sentinel-kind" {
		t.Errorf("formValues mutated: %v", m.formValues)
	}
	if m.formStepIndex != 3 {
		t.Errorf("formStepIndex = %d, want 3 (unchanged)", m.formStepIndex)
	}
	if m.formError != "sentinel-error" {
		t.Errorf("formError = %q, want %q (unchanged)", m.formError, "sentinel-error")
	}
}

func TestMCPAddCmdFor_ReportsHighestShadowingLayer(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.AgentWorkspace,
		`{"mcpServers":{"svc":{"command":"agent-tool"}}}`)
	mcpWriteDoc(t, filepath.Join(paths.Cwd, ".lele"),
		`{"mcpServers":{"svc":{"command":"project-tool"}}}`)
	projectFile := mcp.LayerFile(paths, mcp.LayerProject)
	beforeProject, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("read project fixture: %v", err)
	}
	agentFile := mcp.LayerFile(paths, mcp.LayerAgent)
	beforeAgent, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("read agent fixture: %v", err)
	}

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "svc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("add failed: %v", res.err)
	}
	if res.shadow != mcp.LayerProject {
		t.Errorf("shadow = %q, want %q (highest layer above the target)", res.shadow, mcp.LayerProject)
	}

	afterAgent, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	afterProject, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("re-read project: %v", err)
	}
	if !bytes.Equal(beforeAgent, afterAgent) {
		t.Errorf("agent file modified:\nbefore=%s\nafter=%s", beforeAgent, afterAgent)
	}
	if !bytes.Equal(beforeProject, afterProject) {
		t.Errorf("project file modified:\nbefore=%s\nafter=%s", beforeProject, afterProject)
	}

	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(res)
	base := fmt.Sprintf(i18n.T("tui.mcpAddSuccess"), "svc", mcp.LayerGlobal,
		mcp.LayerFile(paths, mcp.LayerGlobal))
	want := base + " " + fmt.Sprintf(i18n.T("tui.mcpAddShadowSuffix"), mcp.LayerProject)
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	if strings.Count(m.mcpFeedback, fmt.Sprintf(i18n.T("tui.mcpAddShadowSuffix"), mcp.LayerProject)) != 1 {
		t.Errorf("shadow note is not appended exactly once: %q", m.mcpFeedback)
	}
}

func TestMCPAddCmdFor_NeverReportsTheTargetLayerAsShadowing(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir,
		`{"mcpServers":{"svc":{"command":"old-global"}}}`)

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "svc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("add failed: %v", res.err)
	}
	if res.shadow != "" {
		t.Errorf("shadow = %q, want \"\" (the target layer's own copy is a replace, never a shadow)", res.shadow)
	}
	if !res.replaced {
		t.Errorf("replaced = false, want true (the write overwrites the target layer's copy)")
	}
}
