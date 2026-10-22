package tui

// T6 acceptance: the /mcp → N "add an MCP server" wizard's STATE MACHINE
// (plan §6/T6). The pure layer (parsers, masking, renderer) is covered by
// mcp_add_form_test.go and the write path by mcp_add_test.go; this file
// drives the part a user actually exercises — the key walk through
// Update/handleMCPKey/handleMCPAddKey/advanceMCPAdd — which the review
// (B5) found to have zero coverage.
//
// Everything below goes through m.handleKeyMsg (or Update), the same entry
// bubbletea uses, so a regression in the dispatch chain ("n" entry point,
// isFormModal forwarding, the enter/esc ownership) fails here rather than
// shipping silently.

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

// mcpAddWalkStep is one ENTER in the walk: the value typed and the slot the
// cursor must land on after that ENTER.
type mcpAddWalkStep struct {
	value string
	want  int
}

// mcpAddWizardStart opens /mcp and presses "n" through the real key path.
func mcpAddWizardStart(t *testing.T) *Model {
	t.Helper()
	m := mcpAddOpenList(t)
	m.handleKeyMsg(mcpKeyRunes('n'), nil)
	if m.modalMode != ModalMCPAdd {
		t.Fatalf("n did not open the wizard: modalMode = %v", m.modalMode)
	}
	return m
}

// mcpAddTypeAndEnter types a value into the shared input and presses ENTER
// through the real key path (sendKeys' shape, kept local so a change to the
// shared helper cannot silently alter the wizard walk).
func mcpAddTypeAndEnter(t *testing.T, m *Model, value string) *Model {
	t.Helper()
	m.textInput.SetValue(value)
	return sendKeys(m, "\r")
}

// mcpAddWalkTo drives a fresh wizard through steps, asserting the cursor
// slot after every ENTER (the walk contract B1/B3 broke).
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

// ── entry point ───────────────────────────────────────────────────────

// TestMCPAddKeys_NOpensWizard: "n" on the list opens the wizard with the
// cursor on the name slot, the input focused (the blink cmd is returned),
// and the feedback line cleared — DoD 1 / T6 acceptance item 1.
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
	// Opening must NOT wipe the list the wizard returns to (no resetModal).
	if len(m.mcpModalKeys) == 0 {
		t.Error("the /mcp list was wiped by opening the wizard")
	}
}

// TestMCPAddKeys_NWhenPathsUnavailable: when the loop cannot resolve the
// MCP roots, "n" must SAY so on the feedback line instead of opening a
// wizard that cannot finish — the key must never be silently dead
// (startMCPAdd's contract).
func TestMCPAddKeys_NWhenPathsUnavailable(t *testing.T) {
	// A bare AgentLoop resolves no paths (MCPPathsFor → ok=false).
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

// ── forwarding: keys the wizard does NOT own ──────────────────────────

// TestMCPAddKeys_ForwardedKeysTypeAndDoNotNavigate: inside the wizard,
// handleMCPAddKey must return handled=false for every key it does not own
// ("n", "q", up/down) so the isFormModal registration types them — and the
// list cursor behind the modal must not move. This pins both the
// handleMCPAddKey contract and the isFormModal(ModalMCPAdd) registration
// (review M3, which survived because nothing asserted it).
func TestMCPAddKeys_ForwardedKeysTypeAndDoNotNavigate(t *testing.T) {
	m := mcpAddWizardStart(t)
	m.modalSelectedIdx = 1 // the cursor the list is parked on

	// "n" and "q" are regular characters mid-word (server name "quick-srv").
	// Drive through the FULL pipeline (handleKeyMsg), the same entry
	// bubbletea uses: handleMCPAddKey returns handled=false here, and the
	// isFormModal registration at the tail of handleModalKey types it.
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

	// Navigation keys must not move the hidden list cursor.
	before := m.modalSelectedIdx
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyDown}, mcpKeyRunes('j'), mcpKeyRunes('k')} {
		m.handleKeyMsg(msg, nil)
		if m.modalSelectedIdx != before {
			t.Fatalf("%q moved the hidden list cursor to %d", msg.String(), m.modalSelectedIdx)
		}
	}

	// The registration itself: the wizard is a form modal, not a list modal.
	if !isFormModal(ModalMCPAdd, false) {
		t.Error("isFormModal(ModalMCPAdd) = false: typing would be swallowed and \"q\" would close the modal")
	}
	if isListModal(ModalMCPAdd) {
		t.Error("isListModal(ModalMCPAdd) = true: navigation would treat the wizard as a list")
	}
}

// ── the walk ──────────────────────────────────────────────────────────

// TestMCPAddWalk_Stdio: name → kind → command → args → env → description →
// layer → review, asserting formStepIndex after every ENTER.
func TestMCPAddWalk_Stdio(t *testing.T) {
	m := mcpAddWizardStart(t)
	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"fs", mcpAddStepKind},               // name
		{"stdio", mcpAddStepEndpoint},        // kind — ONE ENTER, no stall (B3)
		{"npx", mcpAddStepArgs},              // command
		{"-y,pkg", mcpAddStepEnv},            // args
		{"TOKEN=abc", mcpAddStepDescription}, // env — visited, not skipped (B1)
		{"files", mcpAddStepLayer},           // description
		{"", mcpAddStepReview},               // layer → global default
	})
	if m.modalMode != ModalMCPAdd {
		t.Fatalf("wizard closed early: mode = %v", m.modalMode)
	}
	if got := m.formValue(mcpAddStepEnv); got != "TOKEN=abc" {
		t.Errorf("env slot = %q, want the raw pair text kept for masking (B2)", got)
	}
}

// TestMCPAddWalk_Remote: name → kind → url → type → env → headers →
// description → layer → review (the remote set skips args and includes
// type + headers).
func TestMCPAddWalk_Remote(t *testing.T) {
	m := mcpAddWizardStart(t)
	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"tracker", mcpAddStepKind},
		{"remote", mcpAddStepEndpoint}, // ONE ENTER, no stall (B3)
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
	// Remote must never have asked for args (stdio-only).
	if got := m.formValue(mcpAddStepArgs); got != "" {
		t.Errorf("remote walk stored args = %q, want empty", got)
	}
	if got := m.formValue(mcpAddStepHeaders); got != "Authorization=Bearer xyz" {
		t.Errorf("headers slot = %q, want the raw pair text kept for masking (B2)", got)
	}
}

// ── validation keeps the user on the offending slot ───────────────────

// TestMCPAddWalk_ValidationKeepsCursor: a refused value sets formError and
// does NOT advance — the user edits in place (T6 acceptance: name rules).
func TestMCPAddWalk_ValidationKeepsCursor(t *testing.T) {
	m := mcpAddWizardStart(t)

	// Empty name.
	m = mcpAddTypeAndEnter(t, m, "")
	if m.formStepIndex != mcpAddStepName || m.formError == "" {
		t.Fatalf("empty name: step=%d err=%q, want stay on name with an error",
			m.formStepIndex, m.formError)
	}

	// Slash in the name.
	m = mcpAddTypeAndEnter(t, m, "a/b")
	if m.formStepIndex != mcpAddStepName || m.formError == "" {
		t.Fatalf("slash name: step=%d err=%q, want stay on name with an error",
			m.formStepIndex, m.formError)
	}

	// A valid name clears the error and advances.
	m = mcpAddTypeAndEnter(t, m, "a b")
	if m.formStepIndex != mcpAddStepKind || m.formError != "" {
		t.Fatalf("valid name: step=%d err=%q, want advance to kind with no error",
			m.formStepIndex, m.formError)
	}
}

// ── esc cancels with no write ─────────────────────────────────────────

// TestMCPAddWalk_EscCancelsWithNoWrite: ESC at any slot tears the wizard
// down (back to the list) and never fires a write cmd.
func TestMCPAddWalk_EscCancelsWithNoWrite(t *testing.T) {
	for _, slot := range []int{mcpAddStepName, mcpAddStepEnv, mcpAddStepReview} {
		m := mcpAddWizardStart(t)
		// Park mid-wizard by hand (reaching every slot through ENTER is
		// the walk's job — this test only needs a non-start slot).
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

// ── review confirm gate ───────────────────────────────────────────────

// mcpAddReviewReady walks a fresh wizard to the review slot through the
// real path so the gate is exercised from a real state.
func mcpAddReviewReady(t *testing.T) *Model {
	t.Helper()
	m := mcpAddWizardStart(t)
	return mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"svc", mcpAddStepKind},
		{"stdio", mcpAddStepEndpoint},
		{"npx", mcpAddStepArgs},
		{"", mcpAddStepEnv},         // empty env is valid (optional field)
		{"", mcpAddStepDescription}, // env is consumed by the step above; next is description
		{"desc", mcpAddStepLayer},
		{"", mcpAddStepReview}, // empty layer → global default
	})
}

// mcpAddConfirm types the given answer at the review slot and presses ENTER
// through the real key path, returning the cmd the confirm gate produced
// (nil for "no"/refusal, the async write for "yes").
func mcpAddConfirm(t *testing.T, m *Model, answer string) tea.Cmd {
	t.Helper()
	if m.formStepIndex != mcpAddStepReview {
		t.Fatalf("precondition: cursor at %d, want the review slot", m.formStepIndex)
	}
	m.textInput.SetValue(answer)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	returned := updated.(*Model)
	// The wizard closes on "yes"/"no" (D12) — carry the state back so the
	// caller can assert it; the cmd is what the gate returned.
	*m = *returned
	return cmd
}

// TestMCPAddReviewGate_BareEnterIsRefused: D4 — an empty ENTER at review is
// NOT an implicit yes; the gate refuses it with the localized error and
// fires no cmd.
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

// TestMCPAddReviewGate_NoCancelsWithoutWrite: a typed "no" cancels — back
// to the list, wizard state torn down, no write cmd fired.
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

// TestMCPAddReviewGate_YesFiresWriteCmd: a typed "yes" tears the wizard
// down optimistically (D12) and the confirm gate returns the async write
// cmd.
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

// ── end-to-end: a stdio run writes the typed env to disk ──────────────

// TestMCPAddEndToEnd_StdioWritesTypedEnv: the whole path — keys → walk →
// confirm → the write cmd → on-disk JSON — must carry the env the user
// typed (B2's regression) with its literal ${TOKEN} (D8: no expansion), and
// must NOT carry remote-only fields (D7/XOR).
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

	// Confirm: the wizard closes and the write cmd is produced by the
	// confirm gate. Drive it through Update so the real ENTER routing runs.
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
	// The typed env VALUE and its literal ${TOKEN} (D8: no expansion).
	// Parsed, not string-matched: env is a JSON object whose key order is
	// not guaranteed.
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
	// XOR: a stdio run must never write url/type/headers (D7).
	if e.URL != "" || e.Type != "" || len(e.Headers) != 0 {
		t.Errorf("stdio run leaked remote fields: url=%q type=%q headers=%v",
			e.URL, e.Type, e.Headers)
	}
}

// TestMCPAddEndToEnd_RemoteWritesTypeEnvHeaders is the remote twin of the
// stdio test above (review #2 MINOR 4): the whole path — keys → walk →
// confirm → the write cmd → on-disk JSON — must carry url, type and the
// typed env/headers with their literal ${TOKEN} (D8: no expansion), and
// must NOT carry stdio-only fields (D7/XOR). The headers step is the piece
// the stdio walk cannot reach, so this is what pins B2 for the remote half.
func TestMCPAddEndToEnd_RemoteWritesTypeEnvHeaders(t *testing.T) {
	m := mcpAddWizardStart(t)
	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("fixture lost MCP paths")
	}

	m = mcpAddWalkTo(t, m, []mcpAddWalkStep{
		{"rem", mcpAddStepKind},
		{"remote", mcpAddStepEndpoint}, // ONE ENTER, no stall (B3)
		{"https://e.com/mcp", mcpAddStepType},
		{"sse", mcpAddStepEnv},
		{"TOKEN=${TOKEN}", mcpAddStepHeaders},
		{"Authorization=Bearer abc=def,X-Trace=1", mcpAddStepDescription},
		{"remote end to end", mcpAddStepLayer},
		{"", mcpAddStepReview},
	})

	// Confirm: the wizard closes and the write cmd is produced by the
	// confirm gate. Drive it through Update so the real ENTER routing runs.
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
	// Parsed, not string-matched: env/headers are JSON objects whose key
	// order is not guaranteed.
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
	// XOR: a remote run must never write command/args (D7).
	if e.Command != "" || len(e.Args) != 0 {
		t.Errorf("remote run leaked stdio fields: command=%q args=%v", e.Command, e.Args)
	}
}

// ── secrecy: the wizard never renders an env/header value (M1) ────────

// TestMCPAddWalk_SecretsMaskedEverywhere drives a REAL walk with a secret
// in both env and headers and scans every rendered frame (ANSI-stripped)
// for the raw value. It pins the doc claim (docs/mcp.md "Secrecy") end to
// end: the in-flight input line (password echo), the completed-step
// summary and the review block all mask the value — never echoing it.
func TestMCPAddWalk_SecretsMaskedEverywhere(t *testing.T) {
	const envSecret = "s3cr3t-env-value"
	const hdrSecret = "s3cr3t-hdr-value"
	m := mcpAddWizardStart(t)

	// Walk to the env step with a remote run (so both pair slots are asked).
	for _, step := range []mcpAddWalkStep{
		{"sec", mcpAddStepKind},
		{"remote", mcpAddStepEndpoint},
		{"https://e.com/mcp", mcpAddStepType},
		{"http", mcpAddStepEnv}, // type → env
	} {
		m = mcpAddTypeAndEnter(t, m, step.value)
		if m.formStepIndex != step.want {
			t.Fatalf("walk broke at %q: step=%d want=%d", step.value, m.formStepIndex, step.want)
		}
	}
	if m.formStepIndex != mcpAddStepEnv {
		t.Fatalf("precondition: cursor at %d, want the env slot %d", m.formStepIndex, mcpAddStepEnv)
	}
	// On the env step: type the secret and assert the live input is masked.
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

	// Headers step: same treatment.
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

	// Description and review: both secrets must stay masked on every frame.
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
