package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type mcpAddWalkStep struct {
	value string
	want  int
}

func mcpAddWizardStart(t *testing.T) *Model {
	t.Helper()
	m := mcpAddOpenList(t)
	m.handleKeyMsg(mcpKeyRunes('n'), nil)
	if m.modalMode != ModalMCPAdd {
		t.Fatalf("n did not open the wizard: modalMode = %v", m.modalMode)
	}
	return m
}

func mcpAddTypeAndEnter(t *testing.T, m *Model, value string) *Model {
	t.Helper()
	m.textInput.SetValue(value)
	return sendKeys(m, "\r")
}

func mcpAddWalkTo(t *testing.T, m *Model, steps []mcpAddWalkStep) *Model {
	t.Helper()
	for i, step := range steps {
		m = mcpAddTypeAndEnter(t, m, step.value)
		if m.formError != "" {
			t.Fatalf("step %d (%q): unexpected error %q", i, step.value, m.formError)
		}
		if m.formStepIndex != step.want {
			t.Fatalf("step %d (%q): formStepIndex = %d, want %d (kind=%q)",
				i, step.value, m.formStepIndex, step.want, m.mcpAddTransport())
		}
	}
	return m
}

func TestMCPAddKeys_NOpensWizard(t *testing.T) {
	m := mcpAddOpenList(t)
	m.mcpFeedback = "stale"

	cmd, handled := m.handleMCPKey(mcpKeyRunes('n'))
	if !handled {
		t.Fatal("n not handled in the /mcp list")
	}
	if cmd == nil {
		t.Error("n returned no focus blink cmd (cursor would not blink)")
	}
	if m.modalMode != ModalMCPAdd {
		t.Errorf("modalMode = %v, want ModalMCPAdd", m.modalMode)
	}
	if m.formStepIndex != mcpAddStepName {
		t.Errorf("formStepIndex = %d, want the name slot %d", m.formStepIndex, mcpAddStepName)
	}
	if !m.textInput.Focused() {
		t.Error("shared text input not focused after opening the wizard")
	}
	if m.mcpFeedback != "" {
		t.Errorf("mcpFeedback = %q, want cleared when the wizard opens", m.mcpFeedback)
	}
	if len(m.mcpModalKeys) == 0 {
		t.Error("the /mcp list was wiped by opening the wizard")
	}
}

func TestMCPAddKeys_NWhenPathsUnavailable(t *testing.T) {
	m := &Model{agentLoop: &agent.AgentLoop{}, modalMode: ModalMCP}
	m.mcpModalKeys = []string{"alpha"}
	m.modalItems = []string{"alpha"}
	m.mcpFeedback = "stale"

	cmd, handled := m.handleMCPKey(mcpKeyRunes('n'))
	if !handled {
		t.Fatal("n not handled in the /mcp list")
	}
	if cmd != nil {
		t.Errorf("n returned a cmd, want nil (nothing to open)")
	}
	if m.modalMode != ModalMCP {
		t.Errorf("modalMode = %v, want to stay ModalMCP", m.modalMode)
	}
	if m.mcpFeedback != i18n.T("tui.mcpUnavailable") {
		t.Errorf("mcpFeedback = %q, want %q", m.mcpFeedback, i18n.T("tui.mcpUnavailable"))
	}
	if len(m.formValues) != 0 || m.formStepIndex != 0 {
		t.Errorf("wizard state leaked: formValues=%v step=%d, want none",
			m.formValues, m.formStepIndex)
	}
}

func TestMCPAddKeys_ForwardedKeysTypeAndDoNotNavigate(t *testing.T) {
	m := mcpAddWizardStart(t)
	m.modalSelectedIdx = 1

	for _, msg := range []tea.KeyMsg{mcpKeyRunes('n'), mcpKeyRunes('q'), mcpKeyRunes('u')} {
		if cmd, handled := m.handleMCPAddKey(msg); handled || cmd != nil {
			t.Fatalf("%q: handled=%v cmd=%v, want forwarded (handled=false, nil cmd)",
				msg.String(), handled, cmd)
		}
		m.handleKeyMsg(msg, nil)
	}
	if got := m.textInput.Value(); got != "nqu" {
		t.Errorf("text input = %q, want the forwarded keys to type (\"nqu\")", got)
	}

	before := m.modalSelectedIdx
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyDown}, mcpKeyRunes('j'), mcpKeyRunes('k')} {
		m.handleKeyMsg(msg, nil)
		if m.modalSelectedIdx != before {
			t.Fatalf("%q moved the hidden list cursor to %d", msg.String(), m.modalSelectedIdx)
		}
	}

	if !isFormModal(ModalMCPAdd, false) {
		t.Error("isFormModal(ModalMCPAdd) = false: typing would be swallowed and \"q\" would close the modal")
	}
	if isListModal(ModalMCPAdd) {
		t.Error("isListModal(ModalMCPAdd) = true: navigation would treat the wizard as a list")
	}
}

func TestMCPAddWalk_Stdio(t *testing.T) {
	m := mcpAddWizardStart(t)
	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"fs", mcpAddStepKind},
		{"stdio", mcpAddStepEndpoint},
		{"npx", mcpAddStepArgs},
		{"-y,pkg", mcpAddStepEnv},
		{"TOKEN=abc", mcpAddStepDescription},
		{"files", mcpAddStepLayer},
		{"", mcpAddStepReview},
	})
	if m.modalMode != ModalMCPAdd {
		t.Fatalf("wizard closed early: mode = %v", m.modalMode)
	}
	if got := m.formValue(mcpAddStepEnv); got != "TOKEN=abc" {
		t.Errorf("env slot = %q, want the raw pair text kept for masking (B2)", got)
	}
}

func TestMCPAddWalk_Remote(t *testing.T) {
	m := mcpAddWizardStart(t)
	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"tracker", mcpAddStepKind},
		{"remote", mcpAddStepEndpoint},
		{"https://e.com/mcp", mcpAddStepType},
		{"http", mcpAddStepEnv},
		{"TOKEN=abc", mcpAddStepHeaders},
		{"Authorization=Bearer xyz", mcpAddStepDescription},
		{"tracks things", mcpAddStepLayer},
		{"", mcpAddStepReview},
	})
	if m.modalMode != ModalMCPAdd {
		t.Fatalf("wizard closed early: mode = %v", m.modalMode)
	}
	if got := m.formValue(mcpAddStepArgs); got != "" {
		t.Errorf("remote walk stored args = %q, want empty", got)
	}
	if got := m.formValue(mcpAddStepHeaders); got != "Authorization=Bearer xyz" {
		t.Errorf("headers slot = %q, want the raw pair text kept for masking (B2)", got)
	}
}

func TestMCPAddWalk_ValidationKeepsCursor(t *testing.T) {
	m := mcpAddWizardStart(t)

	m = mcpAddTypeAndEnter(t, m, "")
	if m.formStepIndex != mcpAddStepName || m.formError == "" {
		t.Fatalf("empty name: step=%d err=%q, want stay on name with an error",
			m.formStepIndex, m.formError)
	}

	m = mcpAddTypeAndEnter(t, m, "a/b")
	if m.formStepIndex != mcpAddStepName || m.formError == "" {
		t.Fatalf("slash name: step=%d err=%q, want stay on name with an error",
			m.formStepIndex, m.formError)
	}

	m = mcpAddTypeAndEnter(t, m, "a b")
	if m.formStepIndex != mcpAddStepKind || m.formError != "" {
		t.Fatalf("valid name: step=%d err=%q, want advance to kind with no error",
			m.formStepIndex, m.formError)
	}
}

func TestMCPAddWalk_EscCancelsWithNoWrite(t *testing.T) {
	for _, slot := range []int{mcpAddStepName, mcpAddStepEnv, mcpAddStepReview} {
		m := mcpAddWizardStart(t)
		m.formValues[mcpAddStepName] = "svc"
		m.formValues[mcpAddStepKind] = "stdio"
		m.formStepIndex = slot
		m.mcpFeedback = "sentinel"

		cmd, handled := m.handleMCPAddKey(tea.KeyMsg{Type: tea.KeyEsc})
		if !handled || cmd != nil {
			t.Fatalf("slot %d: esc handled=%v cmd=%v, want (true, nil)", slot, handled, cmd)
		}
		if m.modalMode != ModalMCP {
			t.Errorf("slot %d: modalMode = %v, want back to ModalMCP", slot, m.modalMode)
		}
		if m.formValues != nil {
			t.Errorf("slot %d: formValues = %v, want nil (torn down)", slot, m.formValues)
		}
		if m.mcpFeedback != "sentinel" {
			t.Errorf("slot %d: mcpFeedback = %q, want untouched (no reload, no write)",
				slot, m.mcpFeedback)
		}
	}
}

func mcpAddReviewReady(t *testing.T) *Model {
	t.Helper()
	m := mcpAddWizardStart(t)
	return mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"svc", mcpAddStepKind},
		{"stdio", mcpAddStepEndpoint},
		{"npx", mcpAddStepArgs},
		{"", mcpAddStepEnv},
		{"", mcpAddStepDescription},
		{"desc", mcpAddStepLayer},
		{"", mcpAddStepReview},
	})
}

func mcpAddConfirm(t *testing.T, m *Model, answer string) tea.Cmd {
	t.Helper()
	if m.formStepIndex != mcpAddStepReview {
		t.Fatalf("precondition: cursor at %d, want the review slot", m.formStepIndex)
	}
	m.textInput.SetValue(answer)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	returned := updated.(*Model)
	*m = *returned
	return cmd
}

func TestMCPAddReviewGate_BareEnterIsRefused(t *testing.T) {
	m := mcpAddReviewReady(t)
	cmd, handled := m.handleMCPAddKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("enter at review not handled")
	}
	if cmd != nil {
		t.Errorf("bare ENTER fired a cmd (%v), want none (no write)", cmd)
	}
	if m.formError != i18n.T("tui.mcpAddErrConfirm") {
		t.Errorf("formError = %q, want %q", m.formError, i18n.T("tui.mcpAddErrConfirm"))
	}
	if m.modalMode != ModalMCPAdd {
		t.Errorf("modalMode = %v, want the wizard to stay open", m.modalMode)
	}
}

func TestMCPAddReviewGate_NoCancelsWithoutWrite(t *testing.T) {
	m := mcpAddReviewReady(t)
	cmd := mcpAddConfirm(t, m, "no")
	if cmd != nil {
		t.Errorf("confirm \"no\" returned a cmd (%v), want none (no write)", cmd)
	}
	if m.modalMode != ModalMCP {
		t.Errorf("modalMode = %v, want ModalMCP (cancelled)", m.modalMode)
	}
	if m.formValues != nil {
		t.Errorf("formValues = %v, want nil after cancel", m.formValues)
	}
}

func TestMCPAddReviewGate_YesFiresWriteCmd(t *testing.T) {
	m := mcpAddReviewReady(t)
	cmd := mcpAddConfirm(t, m, "yes")

	if m.modalMode != ModalMCP {
		t.Errorf("modalMode = %v, want ModalMCP (closed optimistically before the write)", m.modalMode)
	}
	if m.formValues != nil {
		t.Errorf("formValues = %v, want nil (torn down before the async write)", m.formValues)
	}
	if cmd == nil {
		t.Fatal("confirm \"yes\" returned no cmd, want the async write cmd")
	}
	if _, ok := cmd().(mcpAddResultMsg); !ok {
		t.Errorf("cmd returned %T, want mcpAddResultMsg", cmd())
	}
}

func TestMCPAddEndToEnd_StdioWritesTypedEnv(t *testing.T) {
	m := mcpAddWizardStart(t)
	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("fixture lost MCP paths")
	}

	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"e2e", mcpAddStepKind},
		{"stdio", mcpAddStepEndpoint},
		{"npx", mcpAddStepArgs},
		{"-y,pkg", mcpAddStepEnv},
		{"TOKEN=${TOKEN},LOG=debug", mcpAddStepDescription},
		{"end to end", mcpAddStepLayer},
		{"", mcpAddStepReview},
	})

	cmd := mcpAddConfirm(t, m, "yes")
	if m.modalMode != ModalMCP {
		t.Fatalf("wizard did not close on confirm: mode = %v", m.modalMode)
	}
	if cmd == nil {
		t.Fatal("confirm \"yes\" returned no write cmd")
	}
	res, ok := cmd().(mcpAddResultMsg)
	if !ok {
		t.Fatalf("write cmd returned %T, want mcpAddResultMsg", cmd())
	}
	if res.err != nil {
		t.Fatalf("write failed: %v", res.err)
	}
	if !res.created || !res.changed {
		t.Errorf("result = created=%v changed=%v, want created && changed", res.created, res.changed)
	}

	after, err := os.ReadFile(filepath.Join(paths.LeleDir, "mcp.json"))
	if err != nil {
		t.Fatalf("read global layer: %v", err)
	}
	var doc struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Type    string            `json:"type"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(after, &doc); err != nil {
		t.Fatalf("written JSON does not parse: %v\n%s", err, after)
	}
	e, ok := doc.Servers["e2e"]
	if !ok {
		t.Fatalf("e2e entry missing:\n%s", after)
	}
	if got := e.Env["TOKEN"]; got != "${TOKEN}" {
		t.Errorf("env TOKEN = %q, want the literal ${TOKEN} (no expansion, D8)", got)
	}
	if got := e.Env["LOG"]; got != "debug" {
		t.Errorf("env LOG = %q, want \"debug\"", got)
	}
	if e.Command != "npx" {
		t.Errorf("command = %q, want npx", e.Command)
	}
	if e.URL != "" || e.Type != "" || len(e.Headers) != 0 {
		t.Errorf("stdio run leaked remote fields: url=%q type=%q headers=%v",
			e.URL, e.Type, e.Headers)
	}
}

func TestMCPAddEndToEnd_RemoteWritesTypeEnvHeaders(t *testing.T) {
	m := mcpAddWizardStart(t)
	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("fixture lost MCP paths")
	}

	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"rem", mcpAddStepKind},
		{"remote", mcpAddStepEndpoint},
		{"https://e.com/mcp", mcpAddStepType},
		{"sse", mcpAddStepEnv},
		{"TOKEN=${TOKEN}", mcpAddStepHeaders},
		{"Authorization=Bearer abc=def,X-Trace=1", mcpAddStepDescription},
		{"remote end to end", mcpAddStepLayer},
		{"", mcpAddStepReview},
	})

	cmd := mcpAddConfirm(t, m, "yes")
	if m.modalMode != ModalMCP {
		t.Fatalf("wizard did not close on confirm: mode = %v", m.modalMode)
	}
	if cmd == nil {
		t.Fatal("confirm \"yes\" returned no write cmd")
	}
	res, ok := cmd().(mcpAddResultMsg)
	if !ok {
		t.Fatalf("write cmd returned %T, want mcpAddResultMsg", cmd())
	}
	if res.err != nil {
		t.Fatalf("write failed: %v", res.err)
	}
	if !res.created || !res.changed {
		t.Errorf("result = created=%v changed=%v, want created && changed", res.created, res.changed)
	}

	after, err := os.ReadFile(filepath.Join(paths.LeleDir, "mcp.json"))
	if err != nil {
		t.Fatalf("read global layer: %v", err)
	}
	var doc struct {
		Servers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Type    string            `json:"type"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(after, &doc); err != nil {
		t.Fatalf("written JSON does not parse: %v\n%s", err, after)
	}
	e, ok := doc.Servers["rem"]
	if !ok {
		t.Fatalf("rem entry missing:\n%s", after)
	}
	if e.URL != "https://e.com/mcp" {
		t.Errorf("url = %q, want %q", e.URL, "https://e.com/mcp")
	}
	if e.Type != "sse" {
		t.Errorf("type = %q, want \"sse\"", e.Type)
	}
	if got := e.Env["TOKEN"]; got != "${TOKEN}" {
		t.Errorf("env TOKEN = %q, want the literal ${TOKEN} (no expansion, D8)", got)
	}
	if got := e.Headers["Authorization"]; got != "Bearer abc=def" {
		t.Errorf("headers[Authorization] = %q, want %q (split on first '=', value keeps its own '=')",
			got, "Bearer abc=def")
	}
	if got := e.Headers["X-Trace"]; got != "1" {
		t.Errorf("headers[X-Trace] = %q, want \"1\"", got)
	}
	if e.Command != "" || len(e.Args) != 0 {
		t.Errorf("remote run leaked stdio fields: command=%q args=%v", e.Command, e.Args)
	}
}

func TestMCPAddWalk_SecretsMaskedEverywhere(t *testing.T) {
	const envSecret = "s3cr3t-env-value"
	const hdrSecret = "s3cr3t-hdr-value"
	m := mcpAddWizardStart(t)

	for _, step := range []mcpAddWalkStep{
		{"sec", mcpAddStepKind},
		{"remote", mcpAddStepEndpoint},
		{"https://e.com/mcp", mcpAddStepType},
		{"http", mcpAddStepEnv},
	} {
		m = mcpAddTypeAndEnter(t, m, step.value)
		if m.formStepIndex != step.want {
			t.Fatalf("walk broke at %q: step=%d want=%d", step.value, m.formStepIndex, step.want)
		}
	}
	if m.formStepIndex != mcpAddStepEnv {
		t.Fatalf("precondition: cursor at %d, want the env slot %d", m.formStepIndex, mcpAddStepEnv)
	}
	m.textInput.SetValue("TOKEN=" + envSecret)
	m.syncTextInputEcho()
	if got := m.textInput.EchoMode; got != textinput.EchoPassword {
		t.Fatalf("env step echo = %v, want EchoPassword (doc: masked as typed)", got)
	}
	if out := mcpAddStripANSI(m.renderMCPAddModal()); strings.Contains(out, envSecret) {
		t.Fatalf("env secret leaked into the live frame:\n%s", out)
	}
	m = sendKeys(m, "\r")
	if m.formStepIndex != mcpAddStepHeaders {
		t.Fatalf("after env: step=%d, want headers(%d)", m.formStepIndex, mcpAddStepHeaders)
	}

	m.textInput.SetValue("Authorization=Bearer " + hdrSecret)
	m.syncTextInputEcho()
	if got := m.textInput.EchoMode; got != textinput.EchoPassword {
		t.Fatalf("headers step echo = %v, want EchoPassword", got)
	}
	if out := mcpAddStripANSI(m.renderMCPAddModal()); strings.Contains(out, hdrSecret) {
		t.Fatalf("header secret leaked into the live frame:\n%s", out)
	}
	m = sendKeys(m, "\r")
	if m.formStepIndex != mcpAddStepDescription {
		t.Fatalf("after headers: step=%d, want description(%d)", m.formStepIndex, mcpAddStepDescription)
	}

	for _, step := range []mcpAddWalkStep{
		{"desc", mcpAddStepLayer},
		{"", mcpAddStepReview},
	} {
		m = mcpAddTypeAndEnter(t, m, step.value)
		if m.formStepIndex != step.want {
			t.Fatalf("walk broke at %q: step=%d want=%d", step.value, m.formStepIndex, step.want)
		}
		out := mcpAddStripANSI(m.renderMCPAddModal())
		for _, secret := range []string{envSecret, hdrSecret} {
			if strings.Contains(out, secret) {
				t.Fatalf("secret %q leaked at slot %d:\n%s", secret, m.formStepIndex, out)
			}
		}
	}
}
