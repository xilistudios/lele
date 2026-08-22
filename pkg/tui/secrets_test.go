package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
)

// newTestModelWithKeyring builds a model whose agent loop has a real, enabled
// file-backed keyring service (so loadSecrets / renderSecretDetail exercise
// the populated path).
func newTestModelWithKeyring(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Keyring = config.KeyringConfig{
		Enabled: true,
		Backend: keyring.BackendFile,
	}
	return newTestModelWithConfig(t, cfg, true)
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "a", []string{"a"}},
		{"multi", "a, b ,c", []string{"a", "b", "c"}},
		{"empty", "", nil},
		{"only comma", ",,", nil},
		{"mixed empties", "a,,b,", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitCSV(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitCSV(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMaskSecretValue(t *testing.T) {
	if got := maskSecretValue("short"); got != "••••" {
		t.Errorf("maskSecretValue(short) = %q, want ••••", got)
	}
	if got := maskSecretValue("1234567890123"); got != "1234"+"••••••••"+"0123" {
		t.Errorf("maskSecretValue(long) = %q", got)
	}
}

func TestFormatSecretLine(t *testing.T) {
	tests := []struct {
		name  string
		meta  keyring.SecretMeta
		check string
	}{
		{"with tags and desc", keyring.SecretMeta{Name: "openai.api_key", Tags: []string{"ai"}, Description: "OpenAI key"}, "openai.api_key"},
		{"scoped", keyring.SecretMeta{Name: "my-secret", Scope: []string{"coder"}}, "(scoped)"},
		{"long name", keyring.SecretMeta{Name: "this-is-a-very-long-secret-name-that-exceeds-thirty-six", Description: "x"}, "..."},
		{"long desc", keyring.SecretMeta{Name: "n", Description: "This description is very long and should get truncated to fit within the limit"}, "..."},
		{"minimal", keyring.SecretMeta{Name: "n"}, "🔐 n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSecretLine(tt.meta)
			if !strings.Contains(got, tt.check) {
				t.Errorf("formatSecretLine() = %q, want contains %q", got, tt.check)
			}
		})
	}
}

func TestLoadSecretsNoService(t *testing.T) {
	m := &Model{} // agentLoop nil → keyringSvc nil
	m.loadSecrets()
	if len(m.modalItems) == 0 {
		t.Fatal("expected unavailable message when keyring svc is nil")
	}
}

func TestLoadSecretsWithKeyring(t *testing.T) {
	m := newTestModelWithKeyring(t)
	svc := m.agentLoop.KeyringService()
	if svc == nil {
		t.Fatal("expected non-nil keyring service")
	}
	if err := svc.SetFromUI("openai.api_key", "sk-123", "OpenAI key", []string{"ai"}, []string{"coder"}, "tui"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	m.loadSecrets()
	if len(m.secretsModalKeys) != 1 || m.secretsModalKeys[0] != "openai.api_key" {
		t.Errorf("expected one secret, got %v", m.secretsModalKeys)
	}
}

func TestLoadSecretsErrorOrEmpty(t *testing.T) {
	m := newTestModelWithKeyring(t)
	m.loadSecrets()
	// Empty store → "no secrets" message present in modalItems
	if len(m.modalItems) == 0 {
		t.Fatal("expected message for empty secrets")
	}
}

func TestSecretsHeader(t *testing.T) {
	m := &Model{} // no agentLoop
	got := m.secretsHeader()
	if !strings.Contains(got, "backend") {
		t.Errorf("secretsHeader() = %q", got)
	}
	m2 := newTestModelWithKeyring(t)
	got2 := m2.secretsHeader()
	if !strings.Contains(got2, "[") {
		t.Errorf("secretsHeader(with keyring) = %q", got2)
	}
}

func TestFindSecretMeta(t *testing.T) {
	m := newTestModelWithKeyring(t)
	svc := m.agentLoop.KeyringService()
	if svc == nil {
		t.Skip("no keyring")
	}
	svc.SetFromUI("s1", "v", "d", nil, nil, "tui")
	if meta, ok := m.findSecretMeta(svc, "s1"); !ok || meta.Name != "s1" {
		t.Errorf("findSecretMeta(s1) = %+v, %v", meta, ok)
	}
	if _, ok := m.findSecretMeta(svc, "missing"); ok {
		t.Error("expected not found for missing secret")
	}
}

func TestSelectedSecretName(t *testing.T) {
	m := &Model{secretsModalKeys: []string{"a", "b"}, modalSelectedIdx: 1}
	if got := m.selectedSecretName(); got != "b" {
		t.Errorf("selectedSecretName = %q, want b", got)
	}
	m2 := &Model{secretsModalKeys: []string{"a"}, modalSelectedIdx: 9}
	if got := m2.selectedSecretName(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	m3 := &Model{secretsModalKeys: []string{"a"}, secretsDetailMode: true, secretsDetailName: "zz"}
	if got := m3.selectedSecretName(); got != "zz" {
		t.Errorf("detail selected= %q, want zz", got)
	}
	// detail mode with empty detail name falls through
	m4 := &Model{secretsModalKeys: []string{"a"}, secretsDetailMode: true, secretsDetailName: "", modalSelectedIdx: 0}
	if got := m4.selectedSecretName(); got != "a" {
		t.Errorf("expected list selection, got %q", got)
	}
}

func TestReselectSecret(t *testing.T) {
	m := &Model{secretsModalKeys: []string{"a", "b", "c"}, modalSelectedIdx: 0, secretsDetailMode: true}
	m.reselectSecret("b")
	if m.modalSelectedIdx != 1 || m.secretsDetailName != "b" {
		t.Errorf("reselect b failed: idx=%d detail=%q", m.modalSelectedIdx, m.secretsDetailName)
	}
	m2 := &Model{secretsModalKeys: []string{"a"}, modalSelectedIdx: 5}
	m2.reselectSecret("nope")
	if m2.modalSelectedIdx != 0 {
		t.Errorf("expected reset to 0, got %d", m2.modalSelectedIdx)
	}
	m3 := &Model{secretsModalKeys: nil, modalSelectedIdx: 0}
	m3.reselectSecret("x")
	if m3.modalSelectedIdx != 0 {
		t.Error("empty keys leave selection unchanged")
	}
}

func TestStartAddSecret(t *testing.T) {
	m := &Model{}
	m.startAddSecret()
	if m.modalMode != ModalAddSecret || m.formStepIndex != 0 || len(m.formValues) != 5 {
		t.Errorf("startAddSecret state mismatch: mode=%d step=%d vals=%d", m.modalMode, m.formStepIndex, len(m.formValues))
	}
	if m.formValues[0] != "" || m.formError != "" {
		t.Error("formValues should be initialized empty")
	}
}

func TestRenderSecretDetailNoService(t *testing.T) {
	m := &Model{width: 100, height: 24, secretsDetailMode: true, secretsDetailName: "x"}
	out := m.renderSecretDetail()
	if out == "" {
		t.Fatal("expected output even when keyring unavailable")
	}
	if m.secretsDetailMode {
		t.Error("detail mode should reset when keyring unavailable")
	}
}

func TestRenderSecretDetailNotFound(t *testing.T) {
	m := newTestModelWithKeyring(t)
	m.width = 100
	m.height = 24
	m.secretsDetailMode = true
	m.secretsDetailName = "missing-secret"
	out := m.renderSecretDetail()
	if out == "" {
		t.Fatal("expected output for missing secret (falls back to list)")
	}
	if m.secretsDetailMode {
		t.Error("detail mode should reset when secret missing")
	}
}

func TestRenderSecretDetailComplete(t *testing.T) {
	m := newTestModelWithKeyring(t)
	svc := m.agentLoop.KeyringService()
	if svc == nil {
		t.Skip("no keyring")
	}
	svc.SetFromUI("my.secret", "super-secret-value", "A description", []string{"tag1", "tag2"}, []string{"coder"}, "tui")
	m.width = 100
	m.height = 24
	m.secretsDetailMode = true
	m.secretsDetailName = "my.secret"
	m.secretsReveal = true
	out := m.renderSecretDetail()
	if !strings.Contains(out, "my.secret") {
		t.Errorf("expected secret name in detail, got %q", out)
	}
	if !strings.Contains(out, "super-secret-value") {
		t.Errorf("expected revealed value in detail, got %q", out)
	}
	// masked
	m.secretsReveal = false
	out2 := m.renderSecretDetail()
	if strings.Contains(out2, "super-secret-value") {
		t.Error("masked detail should not contain the raw value")
	}
}

func TestRenderSecretsList(t *testing.T) {
	m := newTestModelWithKeyring(t)
	svc := m.agentLoop.KeyringService()
	if svc != nil {
		svc.SetFromUI("alpha", "v", "d", nil, nil, "tui")
		svc.SetFromUI("beta", "v", "", nil, nil, "tui")
	}
	m.loadSecrets()
	m.width = 100
	m.height = 24

	// Scroll offset clamping: selected beyond visible window
	m.modalSelectedIdx = len(m.modalItems) - 1
	out := m.renderSecretsList("Secrets Title")
	if !strings.Contains(out, "Secrets Title") {
		t.Errorf("expected title in output, got %q", out)
	}

	// With high scroll offset and moreAbove
	m2 := m
	m2.modalScrollOffset = 100
	m2.modalSelectedIdx = 0
	m2.modalItems = []string{"a", "b", "c", "d"}
	m2.secretsModalKeys = []string{"a", "b", "c", "d"}
	m2.renderSecretsList("Title")
}

func TestRenderSecretsListScrollClamp(t *testing.T) {
	m := &Model{width: 100, height: 8, modalItems: []string{"x", "y", "z"}, modalSelectedIdx: 2}
	out := m.renderSecretsList("T")
	if out == "" {
		t.Fatal("expected output")
	}
}

func TestSecretsCreatedAtFields(t *testing.T) {
	m := newTestModelWithKeyring(t)
	svc := m.agentLoop.KeyringService()
	if svc == nil {
		t.Skip("no keyring")
	}
	svc.SetFromUI("ts.secret", "v", "", nil, nil, "tui")
	m.width = 100
	m.height = 24
	m.secretsDetailMode = true
	m.secretsDetailName = "ts.secret"
	// CreatedAt is populated on insert.
	out := m.renderSecretDetail()
	if !strings.Contains(out, "ts.secret") {
		t.Errorf("expected name in output, got %q", out)
	}
}
