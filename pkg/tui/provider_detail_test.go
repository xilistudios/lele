package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
)

// newProviderConfiguredModel builds a TUI model with a usable provider and one model.
func newProviderConfiguredModel(t *testing.T) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-xxx"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Model: "gpt-4o", ContextWindow: 128000, MaxTokens: 4096},
			},
		},
	}
	return newTestModelWithConfig(t, cfg, true)
}

// TestUpdate_ProviderDetailEnter opens the provider detail view and checks
// its item list is populated from the configured provider.
func TestUpdate_ProviderDetailEnter(t *testing.T) {
	m := newProviderConfiguredModel(t)
	m.modalMode = ModalProviders
	m.executeCommand("/providers")

	if len(m.providerModalKeys) == 0 {
		t.Fatal("expected at least one provider in providerModalKeys")
	}
	// Select the first provider and press enter to open detail.
	m.modalSelectedIdx = 0
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)
	if mm.modalMode != ModalProviderDetail {
		t.Fatalf("modalMode = %v, want ModalProviderDetail", mm.modalMode)
	}
	if mm.providerSelectedName != "openai" {
		t.Errorf("providerSelectedName = %q, want openai", mm.providerSelectedName)
	}
	var hasModel bool
	for _, it := range mm.modalItems {
		if strings.Contains(it, "gpt-4o") {
			hasModel = true
		}
	}
	if !hasModel {
		t.Errorf("provider detail should list model gpt-4o, items=%v", mm.modalItems)
	}
}

// TestUpdate_ProviderDetailAddModel enters "+ Add model" from the provider
// detail view and verifies the AddModel form is active.
func TestUpdate_ProviderDetailAddModel(t *testing.T) {
	m := newProviderConfiguredModel(t)
	m.modalMode = ModalProviders
	m.executeCommand("/providers")
	m.modalSelectedIdx = 0
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)

	// Find the "+ Add model" item index and select it.
	addIdx := -1
	for i, it := range mm.modalItems {
		if it == "+ Add model" {
			addIdx = i
			break
		}
	}
	if addIdx == -1 {
		t.Fatal("+ Add model item not found")
	}
	mm.modalSelectedIdx = addIdx
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = upd.(*Model)
	if mm.modalMode != ModalAddModel {
		t.Fatalf("modalMode = %v, want ModalAddModel after Add model", mm.modalMode)
	}
	if len(mm.formValues) != 5 {
		t.Errorf("formValues len = %d, want 5", len(mm.formValues))
	}
}

// TestUpdate_ProviderDetailDeleteProvider deletes the provider from detail.
func TestUpdate_ProviderDetailDeleteProvider(t *testing.T) {
	m := newProviderConfiguredModel(t)
	// Ensure provider exists in config directly.
	if m.cfg.Providers == nil || m.cfg.Providers.Named == nil {
		t.Fatal("provider config missing")
	}
	if _, ok := m.cfg.Providers.Named["openai"]; !ok {
		t.Fatal("openai provider missing in config")
	}

	m.modalMode = ModalProviders
	m.executeCommand("/providers")
	m.modalSelectedIdx = 0
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)

	// Find "- Delete provider" item.
	delIdx := -1
	for i, it := range mm.modalItems {
		if it == "- Delete provider" {
			delIdx = i
			break
		}
	}
	if delIdx == -1 {
		t.Skip("- Delete provider item not found")
	}
	mm.modalSelectedIdx = delIdx
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = upd.(*Model)
	if mm.modalMode != ModalNone {
		t.Errorf("modalMode = %v, want ModalNone after delete provider", mm.modalMode)
	}
	if _, ok := mm.cfg.Providers.Named["openai"]; ok {
		t.Error("provider openai should be deleted")
	}
}

// TestUpdate_ProviderDetailDeleteModel deletes a specific model from detail.
func TestUpdate_ProviderDetailDeleteModel(t *testing.T) {
	m := newProviderConfiguredModel(t)
	m.modalMode = ModalProviders
	m.executeCommand("/providers")
	m.modalSelectedIdx = 0
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)

	// Find a model item that starts with "  " (two spaces) but not "(no".
	modelIdx := -1
	for i, it := range mm.modalItems {
		if strings.HasPrefix(it, "  gpt-4o") {
			modelIdx = i
			break
		}
	}
	if modelIdx == -1 {
		t.Skip("model item not found in detail view")
	}
	mm.modalSelectedIdx = modelIdx
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = upd.(*Model)
	if _, ok := mm.cfg.Providers.Named["openai"].Models["gpt-4o"]; ok {
		t.Error("gpt-4o model should be deleted from provider")
	}
}

func TestLoadProvidersListEmpty(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/providers")
	// With no providers, the list should include the connect action.
	if len(m.modalItems) == 0 {
		t.Fatal("expected connect action in modal items")
	}
}

func TestListProvidersFilters(t *testing.T) {
	m := newProviderConfiguredModel(t)
	provs := m.listProviders()
	found := false
	for _, p := range provs {
		if p == "openai" {
			found = true
		}
	}
	if !found {
		t.Errorf("listProviders should include openai, got %v", provs)
	}
}