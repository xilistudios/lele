package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Tests for the provider-detail action rows (index-based ENTER handling) and
// the double-confirmed "d" model deletion:
//
//   - the action rows are matched by providerDetailAddIdx/providerDetailDelIdx
//     (never by localized label strings),
//   - deleting a model requires TWO "d" presses on the SAME row within
//     providerDeleteConfirmWindow (mirrors requestCommandDelete),
//   - an armed delete never follows the cursor to another row,
//   - the view renders a feedback line + localized hints.
//
// Helpers (newEditModelTestModel / openProviderDetail / selectModelRow) come
// from provider_edit_test.go — same package.

// detailModels is a shorthand for the models map of the fixture provider.
func detailModels(m *Model) map[string]config.ProviderModelConfig {
	return m.cfg.Providers.Named["test-provider"].Models
}

// TestProviderDetailDeleteRequiresTwoPresses: the first "d" on a model row
// only arms the confirmation — nothing is removed, feedback is shown and the
// armed alias is recorded.
func TestProviderDetailDeleteRequiresTwoPresses(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail", m.modalMode)
	}
	selectModelRow(t, m, "gpt-4o")

	m = sendKeys(m, "d")

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after first d", m.modalMode)
	}
	models := detailModels(m)
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (first press must not delete): %v", len(models), keysOfModels(models))
	}
	if _, ok := models["gpt-4o"]; !ok {
		t.Fatal(`model "gpt-4o" was deleted by a single "d"`)
	}
	if m.providerFeedback == "" {
		t.Fatal("providerFeedback must be set after the arming press")
	}
	wantFeedback := fmt.Sprintf(i18n.T("tui.modelDeleteConfirm"), "gpt-4o")
	if m.providerFeedback != wantFeedback {
		t.Errorf("providerFeedback = %q, want %q", m.providerFeedback, wantFeedback)
	}
	if m.providerDeleteKey != "gpt-4o" {
		t.Errorf("providerDeleteKey = %q, want gpt-4o", m.providerDeleteKey)
	}
}

// TestProviderDetailDeleteSecondPressRemovesModel: a second "d" on the same
// row within the window removes the model from config AND from the rebuilt
// detail list, leaves its sibling untouched and keeps the detail view open.
func TestProviderDetailDeleteSecondPressRemovesModel(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")

	m = sendKeys(m, "d", "d")

	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after delete", m.modalMode)
	}
	models := detailModels(m)
	if len(models) != 1 {
		t.Fatalf("models count = %d, want 1: %v", len(models), keysOfModels(models))
	}
	if _, ok := models["gpt-4o"]; ok {
		t.Error(`model "gpt-4o" must be gone after the second "d"`)
	}
	if _, ok := models["gpt-4o-mini"]; !ok {
		t.Error("gpt-4o-mini must be untouched by the delete")
	}

	// Gone from the rebuilt list too.
	for _, item := range m.modalItems {
		if item == "  gpt-4o" {
			t.Errorf("rebuilt modalItems still contains the deleted row: %v", m.modalItems)
		}
	}
	for idx, alias := range m.providerDetailRows {
		if alias == "gpt-4o" {
			t.Errorf("providerDetailRows[%d] still maps to deleted alias gpt-4o", idx)
		}
	}
	if m.providerDeleteKey != "" {
		t.Errorf("providerDeleteKey = %q, want disarmed (\"\") after delete", m.providerDeleteKey)
	}
	wantFeedback := fmt.Sprintf("%s: %s", i18n.T("tui.modelDeleted"), "gpt-4o")
	if m.providerFeedback != wantFeedback {
		t.Errorf("providerFeedback = %q, want %q", m.providerFeedback, wantFeedback)
	}

	// Persisted to disk.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if _, ok := reloaded.Providers.Named["test-provider"].Models["gpt-4o"]; ok {
		t.Error("deleted model still present on disk")
	}
	if _, ok := reloaded.Providers.Named["test-provider"].Models["gpt-4o-mini"]; !ok {
		t.Error("sibling model missing from disk")
	}
}

// TestProviderDetailDeleteArmingIsRowScoped: an armed delete must not follow
// the cursor. After navigating away, a single "d" on another model only arms
// THAT model — nothing is deleted, and the armed key tracks the new row.
func TestProviderDetailDeleteArmingIsRowScoped(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	selectModelRow(t, m, "gpt-4o")

	m = sendKeys(m, "d") // arm gpt-4o
	if m.providerDeleteKey != "gpt-4o" {
		t.Fatalf("providerDeleteKey = %q, want gpt-4o", m.providerDeleteKey)
	}

	m = sendKeys(m, "j") // navigate down to gpt-4o-mini (disarms)
	if m.providerDeleteKey != "" {
		t.Fatalf("providerDeleteKey = %q after navigation, want disarmed", m.providerDeleteKey)
	}
	if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o-mini" {
		t.Fatalf("cursor on idx %d (%q), want gpt-4o-mini", m.modalSelectedIdx, alias)
	}

	m = sendKeys(m, "d") // first press on row B: arm B, do NOT delete

	models := detailModels(m)
	if len(models) != 2 {
		t.Fatalf("models count = %d, want 2 (arming must not follow the cursor): %v", len(models), keysOfModels(models))
	}
	if m.providerDeleteKey != "gpt-4o-mini" {
		t.Errorf("providerDeleteKey = %q, want gpt-4o-mini (armed key tracks the new row)", m.providerDeleteKey)
	}
}

// TestProviderDetailDeleteOnNonModelRowIsInert: pressing "d" on an info row
// or a "---" separator never touches the config and explains itself via the
// feedback line.
func TestProviderDetailDeleteOnNonModelRowIsInert(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)

	// Info row (first item is "Type: ...").
	m.modalSelectedIdx = 0
	m = sendKeys(m, "d")
	if len(detailModels(m)) != 2 {
		t.Fatalf(`models changed after "d" on an info row: %v`, keysOfModels(detailModels(m)))
	}
	if m.providerFeedback != i18n.T("tui.modelDeleteNoRow") {
		t.Errorf("providerFeedback = %q, want %q", m.providerFeedback, i18n.T("tui.modelDeleteNoRow"))
	}
	if m.providerDeleteKey != "" {
		t.Errorf("providerDeleteKey = %q on a non-model row, want disarmed", m.providerDeleteKey)
	}

	// Separator row.
	sepIdx := -1
	for i, item := range m.modalItems {
		if item == "---" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatalf(`no "---" separator row in items %v`, m.modalItems)
	}
	m.modalSelectedIdx = sepIdx
	m = sendKeys(m, "d")
	if len(detailModels(m)) != 2 {
		t.Fatalf(`models changed after "d" on a separator: %v`, keysOfModels(detailModels(m)))
	}
	if m.providerFeedback != i18n.T("tui.modelDeleteNoRow") {
		t.Errorf("providerFeedback = %q, want %q", m.providerFeedback, i18n.T("tui.modelDeleteNoRow"))
	}
}

// TestProviderDetailActionRowsUseIndexes: the action rows are located via
// providerDetailAddIdx/providerDetailDelIdx, carry the localized labels, and
// ENTER on the add row opens ModalAddModel with providerSelectedName intact
// (its save path calls addModelToProvider with it).
func TestProviderDetailActionRowsUseIndexes(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)

	if m.providerDetailAddIdx < 0 || m.providerDetailAddIdx >= len(m.modalItems) {
		t.Fatalf("providerDetailAddIdx = %d out of range for %v", m.providerDetailAddIdx, m.modalItems)
	}
	if m.providerDetailDelIdx < 0 || m.providerDetailDelIdx >= len(m.modalItems) {
		t.Fatalf("providerDetailDelIdx = %d out of range for %v", m.providerDetailDelIdx, m.modalItems)
	}
	if got := m.modalItems[m.providerDetailAddIdx]; got != i18n.T("tui.addModelAction") {
		t.Errorf("items[addIdx] = %q, want %q", got, i18n.T("tui.addModelAction"))
	}
	if got := m.modalItems[m.providerDetailDelIdx]; got != i18n.T("tui.deleteProviderAction") {
		t.Errorf("items[delIdx] = %q, want %q", got, i18n.T("tui.deleteProviderAction"))
	}

	m.modalSelectedIdx = m.providerDetailAddIdx
	m = sendKeys(m, "\r")
	if m.modalMode != ModalAddModel {
		t.Fatalf("modal = %v, want ModalAddModel after ENTER on the add row", m.modalMode)
	}
	if m.providerSelectedName != "test-provider" {
		t.Errorf("providerSelectedName = %q, want test-provider (add-model save path needs it)", m.providerSelectedName)
	}
	if m.formStepIndex != 0 {
		t.Errorf("formStepIndex = %d, want 0", m.formStepIndex)
	}
}

// TestProviderDetailHintsRender: the provider-detail view renders the
// localized hints line (and the feedback line when present) and survives a
// provider with zero models.
func TestProviderDetailHintsRender(t *testing.T) {
	m := newEditModelTestModel(t)
	m.width = 120
	m.height = 40
	m = openProviderDetail(m)
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail", m.modalMode)
	}

	out := ansi.Strip(m.renderActiveModal())
	if !strings.Contains(out, i18n.T("tui.providerDetailHints")) {
		t.Errorf("detail view missing hints line %q:\n%s", i18n.T("tui.providerDetailHints"), out)
	}
	if !strings.Contains(out, i18n.T("tui.providerDetail")) {
		t.Errorf("detail view missing title:\n%s", out)
	}

	// Feedback line renders when set.
	m.providerFeedback = "FEEDBACK-CANARY"
	out = ansi.Strip(m.renderActiveModal())
	if !strings.Contains(out, "FEEDBACK-CANARY") {
		t.Errorf("detail view missing feedback line:\n%s", out)
	}

	// Provider with zero models: must render (and show the hints) without panicking.
	empty := newProviderHelpersTestModel(t)
	empty.width = 120
	empty.height = 40
	empty.enterProviderDetail("emptyprov", "")
	emptyOut := ansi.Strip(empty.renderActiveModal())
	if !strings.Contains(emptyOut, "  (no models)") {
		t.Errorf("0-model detail missing placeholder row:\n%s", emptyOut)
	}
	if !strings.Contains(emptyOut, i18n.T("tui.providerDetailHints")) {
		t.Errorf("0-model detail missing hints line:\n%s", emptyOut)
	}
}
