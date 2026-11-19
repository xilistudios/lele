package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Tests for the five reasoning steps of the edit-model form (ModalEditModel):
// the form must let the user override every config.ReasoningConfig field of a
// model, round-trip the stored config on Enter-through, clear an override with
// "default", collapse back to a nil config when nothing is set, and reject an
// invalid answer on the step that caused it. The add-model form and the
// /connect wizard keep their old step counts and their catalog seeding.

// ── helpers ──

// openEditForm opens the pre-filled ModalEditModel form for alias from the
// provider detail view.
func openEditForm(t *testing.T, m *Model, alias string) *Model {
	t.Helper()
	selectModelRow(t, m, alias)
	m = sendKeys(m, "\r")
	if m.modalMode != ModalEditModel {
		t.Fatalf("modal = %v, want ModalEditModel (formError = %q)", m.modalMode, m.formError)
	}
	return m
}

// walkSteps advances from the current step to lastStep inclusive, accepting the
// pre-filled answer unless override holds a value for that step index. It
// returns the model after the ENTER on lastStep (which usually saves).
func walkSteps(t *testing.T, m *Model, lastStep int, override map[int]string) *Model {
	t.Helper()
	for step := m.formStepIndex; step <= lastStep; step++ {
		if m.formStepIndex != step {
			t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
		}
		if v, ok := override[step]; ok {
			m.textInput.SetValue(v)
		}
		m = sendKeys(m, "\r")
	}
	return m
}

// finishEdit answers every remaining step of the edit form (overriding the
// answers present in override) and returns the saved model.
func finishEdit(t *testing.T, m *Model, override map[int]string) *Model {
	t.Helper()
	m = walkSteps(t, m, editModelStepCount-1, override)
	if m.modalMode != ModalProviderDetail {
		t.Fatalf("modal = %v, want ModalProviderDetail after the last step (formError = %q)", m.modalMode, m.formError)
	}
	return m
}

// savedModel returns the stored config of a model alias in the test provider.
func savedModel(t *testing.T, m *Model, alias string) config.ProviderModelConfig {
	t.Helper()
	mc, ok := m.cfg.Providers.Named["test-provider"].Models[alias]
	if !ok {
		t.Fatalf("model %q missing, models = %v", alias, keysOfModels(m.cfg.Providers.Named["test-provider"].Models))
	}
	return mc
}

// reasoningVals builds a formValues-sized slice whose five reasoning steps hold
// the given answers (every other step stays empty — only the reasoning indexes
// are read).
func reasoningVals(enable, effort, maxTokens, exclude, summary string) []string {
	vals := make([]string, editModelStepCount)
	vals[editModelReasoningEnableStep] = enable
	vals[editModelReasoningEffortStep] = effort
	vals[editModelReasoningMaxTokensStep] = maxTokens
	vals[editModelReasoningExcludeStep] = exclude
	vals[editModelReasoningSummaryStep] = summary
	return vals
}

// ── 1. Enter-through round-trip ──

// TestEditModelEnterThroughPreservesReasoning walks all 11 steps with Enter and
// proves nothing is lost: a fully-loaded ReasoningConfig comes back field by
// field, the Temperature/Video fields the form never asks about survive, and the
// result is persisted to disk.
func TestEditModelEnterThroughPreservesReasoning(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	m = openEditForm(t, m, "gpt-4o")

	// Nothing overridden: every step accepts its pre-filled answer.
	m = finishEdit(t, m, nil)

	mc := savedModel(t, m, "gpt-4o")
	assertFullyLoadedReasoning(t, mc.Reasoning)
	if mc.Temperature == nil || *mc.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7 (preserved)", mc.Temperature)
	}
	if !mc.Video {
		t.Error("Video = false, want true (preserved — the form never asks)")
	}
	if mc.Model != "stored-gpt-4o-001" || mc.ContextWindow != 128000 || mc.MaxTokens != 4096 {
		t.Errorf("entry = %+v, want the stored values unchanged", mc)
	}

	// Persisted to disk, not only in memory.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	disk := reloaded.Providers.Named["test-provider"].Models["gpt-4o"]
	assertFullyLoadedReasoning(t, disk.Reasoning)
	if disk.Temperature == nil || *disk.Temperature != 0.7 || !disk.Video {
		t.Errorf("disk entry = %+v, want Temperature 0.7 and Video preserved", disk)
	}
}

// TestEditModelEnterThroughKeepsNilReasoning proves the neutral pre-fill
// (disabled + every override "default") round-trips to nil, so an edit can never
// invent a reasoning config for a model that had none.
func TestEditModelEnterThroughKeepsNilReasoning(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	m = openEditForm(t, m, "gpt-4o-mini")

	// The neutral answers are pre-filled.
	wantReasoningPrefill := map[int]string{
		editModelReasoningEnableStep:    "no",
		editModelReasoningEffortStep:    reasoningFormDefault,
		editModelReasoningMaxTokensStep: "",
		editModelReasoningExcludeStep:   reasoningFormDefault,
		editModelReasoningSummaryStep:   reasoningFormDefault,
	}
	for step, want := range wantReasoningPrefill {
		if got := m.formValues[step]; got != want {
			t.Errorf("formValues[%d] = %q, want %q", step, got, want)
		}
	}

	m = finishEdit(t, m, nil)

	mc := savedModel(t, m, "gpt-4o-mini")
	if mc.Reasoning != nil {
		t.Errorf("Reasoning = %+v, want nil (no override was set)", mc.Reasoning)
	}
	if !mc.Vision || mc.ContextWindow != 64000 {
		t.Errorf("entry = %+v, want the stored values unchanged", mc)
	}
}

// TestEditModelReasoningEnableCreatesConfigFromNil covers the enable option on a
// model that had no ReasoningConfig at all: answering "yes" alone must
// produce a non-nil config with only the flag set, and that config must survive
// the next edit round-trip.
func TestEditModelReasoningEnableCreatesConfigFromNil(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	m = openEditForm(t, m, "gpt-4o-mini")

	m = finishEdit(t, m, map[int]string{editModelReasoningEnableStep: "yes"})

	want := &config.ReasoningConfig{Enable: true}
	assertReasoningEqual(t, savedModel(t, m, "gpt-4o-mini").Reasoning, want)

	// Re-open: the stored flag is now the pre-filled answer, and Enter-through
	// keeps it.
	m = openEditForm(t, m, "gpt-4o-mini")
	if got := m.formValues[editModelReasoningEnableStep]; got != "yes" {
		t.Errorf("formValues[%d] = %q, want yes", editModelReasoningEnableStep, got)
	}
	m = finishEdit(t, m, nil)
	assertReasoningEqual(t, savedModel(t, m, "gpt-4o-mini").Reasoning, want)
}

// ── 2. Per-option edits ──

// TestEditModelReasoningOptionEdits is the table-driven check of every reasoning
// step: a real answer is persisted (and normalized), "default" clears the
// pointer, and disabling with everything else cleared collapses to nil.
func TestEditModelReasoningOptionEdits(t *testing.T) {
	tests := []struct {
		name string
		// answers overrides the pre-filled reasoning answers (indexed by step).
		answers map[int]string
		want    *config.ReasoningConfig
	}{
		{
			name: "effort_low_overrides_stored_high",
			answers: map[int]string{
				editModelReasoningEffortStep: "low",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("low"), MaxTokens: ptrInt(2048), Exclude: ptrBool(true), Summary: ptrString("detailed")},
		},
		{
			name: "effort_case_and_spaces_normalized",
			answers: map[int]string{
				editModelReasoningEffortStep: "  HIGH ",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(2048), Exclude: ptrBool(true), Summary: ptrString("detailed")},
		},
		{
			name: "effort_default_clears_pointer",
			answers: map[int]string{
				editModelReasoningEffortStep: "default",
			},
			want: &config.ReasoningConfig{Enable: true, MaxTokens: ptrInt(2048), Exclude: ptrBool(true), Summary: ptrString("detailed")},
		},
		{
			name: "max_tokens_set",
			answers: map[int]string{
				editModelReasoningMaxTokensStep: "4096",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(4096), Exclude: ptrBool(true), Summary: ptrString("detailed")},
		},
		{
			name: "max_tokens_default_clears_pointer",
			answers: map[int]string{
				editModelReasoningMaxTokensStep: "default",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), Exclude: ptrBool(true), Summary: ptrString("detailed")},
		},
		{
			name: "exclude_no_sets_false",
			answers: map[int]string{
				editModelReasoningExcludeStep: "no",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(2048), Exclude: ptrBool(false), Summary: ptrString("detailed")},
		},
		{
			name: "exclude_default_clears_pointer",
			answers: map[int]string{
				editModelReasoningExcludeStep: "default",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(2048), Summary: ptrString("detailed")},
		},
		{
			name: "summary_concise_overrides",
			answers: map[int]string{
				editModelReasoningSummaryStep: "concise",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(2048), Exclude: ptrBool(true), Summary: ptrString("concise")},
		},
		{
			name: "summary_default_clears_pointer",
			answers: map[int]string{
				editModelReasoningSummaryStep: "default",
			},
			want: &config.ReasoningConfig{Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(2048), Exclude: ptrBool(true)},
		},
		{
			name: "enable_no_keeps_explicit_overrides",
			answers: map[int]string{
				editModelReasoningEnableStep: "no",
			},
			want: &config.ReasoningConfig{Enable: false, Effort: ptrString("high"), MaxTokens: ptrInt(2048), Exclude: ptrBool(true), Summary: ptrString("detailed")},
		},
		{
			name: "enable_no_and_everything_default_collapses_to_nil",
			answers: map[int]string{
				editModelReasoningEnableStep:    "no",
				editModelReasoningEffortStep:    "default",
				editModelReasoningMaxTokensStep: "default",
				editModelReasoningExcludeStep:   "default",
				editModelReasoningSummaryStep:   "default",
			},
			want: nil,
		},
		{
			name: "enable_no_with_one_override_keeps_config",
			answers: map[int]string{
				editModelReasoningEnableStep:    "no",
				editModelReasoningEffortStep:    "default",
				editModelReasoningMaxTokensStep: "default",
				editModelReasoningExcludeStep:   "default",
				editModelReasoningSummaryStep:   "auto",
			},
			want: &config.ReasoningConfig{Enable: false, Summary: ptrString("auto")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newEditModelTestModel(t)
			m = openProviderDetail(m)
			m = openEditForm(t, m, "gpt-4o")
			m = finishEdit(t, m, tc.answers)

			mc := savedModel(t, m, "gpt-4o")
			assertReasoningEqual(t, mc.Reasoning, tc.want)
		})
	}
}

// TestEditModelReasoningAnswersCanonicalizedOnAdvance proves a mixed-case
// keyword answer is stored lowercase in the form state, so the completed-step
// list reads back exactly what will be saved.
func TestEditModelReasoningAnswersCanonicalizedOnAdvance(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	m = openEditForm(t, m, "gpt-4o")

	// Step 6 answered "YES" → stored "yes".
	m = walkSteps(t, m, editModelReasoningEnableStep, map[int]string{
		editModelReasoningEnableStep: "YES",
	})
	if got := m.formValues[editModelReasoningEnableStep]; got != "yes" {
		t.Errorf("formValues[%d] = %q, want yes (canonical)", editModelReasoningEnableStep, got)
	}

	// Step 7 answered "MEDIUM" → stored "medium".
	m.textInput.SetValue("MEDIUM")
	m = sendKeys(m, "\r")
	if m.formStepIndex != editModelReasoningMaxTokensStep {
		t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, editModelReasoningMaxTokensStep, m.formError)
	}
	if got := m.formValues[editModelReasoningEffortStep]; got != "medium" {
		t.Errorf("formValues[%d] = %q, want medium (canonical)", editModelReasoningEffortStep, got)
	}
}

// ── 3. Invalid answers ──

// TestEditModelReasoningInvalidAnswersRejected walks to each reasoning step,
// types an answer the config would reject and asserts the form stays open with
// an error and the stored model is untouched (no partial save).
func TestEditModelReasoningInvalidAnswersRejected(t *testing.T) {
	tests := []struct {
		name  string
		step  int
		value string
	}{
		{"enable_maybe", editModelReasoningEnableStep, "maybe"},
		{"effort_off_is_not_a_model_effort", editModelReasoningEffortStep, "off"},
		{"effort_bogus", editModelReasoningEffortStep, "bogus"},
		{"effort_numeric", editModelReasoningEffortStep, "3"},
		{"max_tokens_zero", editModelReasoningMaxTokensStep, "0"},
		{"max_tokens_negative", editModelReasoningMaxTokensStep, "-5"},
		{"max_tokens_not_a_number", editModelReasoningMaxTokensStep, "abc"},
		{"exclude_maybe", editModelReasoningExcludeStep, "maybe"},
		{"summary_bogus", editModelReasoningSummaryStep, "bogus"},
		{"summary_off", editModelReasoningSummaryStep, "off"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newEditModelTestModel(t)
			before := savedModel(t, m, "gpt-4o")
			m = openProviderDetail(m)
			m = openEditForm(t, m, "gpt-4o")

			// Accept everything up to (not including) the offending step.
			m = walkSteps(t, m, tc.step-1, nil)
			if m.formStepIndex != tc.step {
				t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, tc.step, m.formError)
			}

			m.textInput.SetValue(tc.value)
			m = sendKeys(m, "\r")

			if m.modalMode != ModalEditModel {
				t.Fatalf("modal = %v, want ModalEditModel (the invalid answer must not save)", m.modalMode)
			}
			if m.formStepIndex != tc.step {
				t.Errorf("formStepIndex = %d, want to stay on %d", m.formStepIndex, tc.step)
			}
			if m.formError == "" {
				t.Fatal("formError must be set for an invalid reasoning answer")
			}

			after := savedModel(t, m, "gpt-4o")
			assertReasoningEqual(t, after.Reasoning, before.Reasoning)
			if after.Model != before.Model || after.ContextWindow != before.ContextWindow ||
				after.MaxTokens != before.MaxTokens || after.Temperature == nil {
				t.Errorf("stored model changed by a rejected step: %+v", after)
			}
		})
	}
}

// ── 4. reasoningFromFormValues unit table ──

// TestReasoningFromFormValues covers the pure conversion helper: unset/default
// handling, case-insensitivity, trimming and the nil-collapse rule.
func TestReasoningFromFormValues(t *testing.T) {
	tests := []struct {
		name     string
		vals     []string
		want     *config.ReasoningConfig
		wantErr  string // substring; "" means no error
		wantNils bool   // want is nil
	}{
		{
			name: "everything_default_is_nil",
			vals: reasoningVals("no", "default", "default", "default", "default"),
		},
		{
			name: "blank_everything_is_nil",
			vals: reasoningVals("no", "", "", "", ""),
		},
		{
			name:     "enable_yes_alone_yields_config",
			vals:     reasoningVals("yes", "default", "", "default", "default"),
			want:     &config.ReasoningConfig{Enable: true},
			wantNils: true, // assert the four pointers stay nil
		},
		{
			name: "everything_set",
			vals: reasoningVals("yes", "high", "2048", "yes", "detailed"),
			want: &config.ReasoningConfig{
				Enable: true, Effort: ptrString("high"), MaxTokens: ptrInt(2048),
				Exclude: ptrBool(true), Summary: ptrString("detailed"),
			},
		},
		{
			name: "case_insensitive_and_trimmed",
			vals: reasoningVals(" YES ", " MEDIUM ", " 512 ", " No ", " CONCISE "),
			want: &config.ReasoningConfig{
				Enable: true, Effort: ptrString("medium"), MaxTokens: ptrInt(512),
				Exclude: ptrBool(false), Summary: ptrString("concise"),
			},
		},
		{
			name: "default_is_case_insensitive",
			vals: reasoningVals("no", "DEFAULT", " Default ", "Default", " default "),
		},
		{
			name:    "invalid_enable",
			vals:    reasoningVals("maybe", "default", "", "default", "default"),
			wantErr: "must be yes or no",
		},
		{
			name:    "invalid_effort_off",
			vals:    reasoningVals("yes", "off", "", "default", "default"),
			wantErr: `invalid reasoning effort: "off"`,
		},
		{
			name:    "invalid_effort",
			vals:    reasoningVals("yes", "bogus", "", "default", "default"),
			wantErr: "must be low, medium, high or default",
		},
		{
			name:    "invalid_max_tokens_zero",
			vals:    reasoningVals("yes", "default", "0", "default", "default"),
			wantErr: "must be a positive integer",
		},
		{
			name:    "invalid_max_tokens_negative",
			vals:    reasoningVals("yes", "default", "-1", "default", "default"),
			wantErr: "must be a positive integer",
		},
		{
			name:    "invalid_max_tokens_text",
			vals:    reasoningVals("yes", "default", "abc", "default", "default"),
			wantErr: "must be a positive integer",
		},
		{
			name:    "invalid_exclude",
			vals:    reasoningVals("yes", "default", "", "maybe", "default"),
			wantErr: "must be yes, no or default",
		},
		{
			name:    "invalid_summary",
			vals:    reasoningVals("yes", "default", "", "default", "bogus"),
			wantErr: "must be auto, detailed, concise or default",
		},
		{
			name:    "short_slice_reads_missing_steps_as_blank",
			vals:    []string{}, // no steps at all: enable "" is invalid
			wantErr: "must be yes or no",
		},
		{
			name: "only_enable_step_present",
			// A slice that stops right after the enable step: the four optional
			// steps read as blank (= unset).
			vals: append(make([]string, editModelReasoningEnableStep), "yes"),
			want: &config.ReasoningConfig{Enable: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reasoningFromFormValues(tc.vals)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got nil (config %+v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %q, want it to contain %q", err.Error(), tc.wantErr)
				}
				if got != nil {
					t.Errorf("config = %+v, want nil on error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertReasoningEqual(t, got, tc.want)
			if tc.wantNils && got != nil {
				if got.Effort != nil || got.MaxTokens != nil || got.Exclude != nil || got.Summary != nil {
					t.Errorf("optional overrides = %+v, want all nil", got)
				}
			}
		})
	}
}

// ── 5. Add-model / connect regressions ──

// TestAddModelFormKeepsSixSteps pins the add form: ModalAddModel shows the six
// original steps, saves on the think-system step and offers no reasoning step
// (the reasoning config of an ADDED model keeps being seeded from the catalog by
// addModelToProvider). The /connect wizard (ModalAddProvider) is untouched and
// keeps its own eleven steps.
func TestAddModelFormKeepsSixSteps(t *testing.T) {
	m := newEditModelTestModel(t)

	m.modalMode = ModalAddModel
	m.formValues = make([]string, addModelStepCount)
	steps := m.formStepNames()
	if len(steps) != addModelStepCount {
		t.Fatalf("ModalAddModel formStepNames() = %d steps, want %d: %q", len(steps), addModelStepCount, steps)
	}
	for _, label := range steps {
		if strings.Contains(strings.ToLower(label), "reasoning") {
			t.Errorf("ModalAddModel step %q: the add form must not ask about reasoning", label)
		}
	}
	if got := modelFormLastStep(ModalAddModel); got != addModelThinkSystemStep {
		t.Errorf("modelFormLastStep(ModalAddModel) = %d, want %d", got, addModelThinkSystemStep)
	}
	if got := modelFormLastStep(ModalEditModel); got != editModelReasoningSummaryStep {
		t.Errorf("modelFormLastStep(ModalEditModel) = %d, want %d", got, editModelReasoningSummaryStep)
	}

	// The reasoning steps are never "allowed empty" in the add form.
	for step := editModelReasoningEnableStep; step < editModelStepCount; step++ {
		if modelFormAllowsEmptyValue(ModalAddModel, step) {
			t.Errorf("step %d: the add form must not accept a blank reasoning answer", step)
		}
	}

	// /connect is untouched: its own eleven-step wizard still renders 11 steps.
	m.modalMode = ModalAddProvider
	if got := len(m.formStepNames()); got != 11 {
		t.Errorf("ModalAddProvider formStepNames() = %d steps, want 11", got)
	}

	// And the catalog seeding of Reasoning.Effort on ADD is unchanged: the add
	// flow still lets addModelToProvider derive it from the catalog.
	m.modalMode = ModalNone
	var pick catalog.Model
	for _, cm := range catalog.ModelsForProvider("openai") {
		if len(cm.ThinkingLevels) > 0 {
			pick = cm
			break
		}
	}
	if pick.ID == "" {
		t.Skip("no openai catalog model with thinking levels")
	}
	if err := m.addModelToProvider("test-provider", "seeded", pick.ID, 0, 0, false, ""); err != nil {
		t.Fatalf("addModelToProvider: %v", err)
	}
	seeded := savedModel(t, m, "seeded")
	if seeded.Reasoning == nil || seeded.Reasoning.Effort == nil {
		t.Fatalf("added model must keep the catalog-seeded Reasoning.Effort, got %+v", seeded.Reasoning)
	}
}

// TestEditModelFormRendersReasoningSteps covers the painted surface: the modal
// title, the five reasoning step labels and the step hint must reach the frame,
// so a user can see what the new steps ask and how to clear an override.
func TestEditModelFormRendersReasoningSteps(t *testing.T) {
	m := newEditModelTestModel(t)
	m.width, m.height = 120, 60
	m = openProviderDetail(m)
	m = openEditForm(t, m, "gpt-4o")

	// Land on the reasoning-effort step (7) so its hint is painted.
	m = walkSteps(t, m, editModelReasoningEffortStep-1, nil)

	frame := m.View()
	for _, want := range []string{
		i18n.T("tui.editModel"),
		"Reasoning (yes/no)",
		"Reasoning effort",
		"Reasoning max tokens",
		"Reasoning exclude (yes/no/default)",
		"Reasoning summary",
		i18n.T("tui.editModelReasoningDefaultHint"),
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("edit-model form frame missing %q:\n%s", want, frame)
		}
	}
}

// ── 6/7. Form shape and ESC ──

// TestModelFormStepLabelsMatchStepIndexes pins the ORDER of the labels to the
// step constants: a reordered append (or a step slipped into the middle) would
// otherwise ask the user one thing and store another without any length
// mismatch to catch it.
func TestModelFormStepLabelsMatchStepIndexes(t *testing.T) {
	m := newEditModelTestModel(t)
	m.modalMode = ModalAddModel
	addSteps := m.formStepNames()
	m.modalMode = ModalEditModel
	editSteps := m.formStepNames()

	if len(addSteps) != addModelStepCount {
		t.Fatalf("add steps = %d, want %d: %q", len(addSteps), addModelStepCount, addSteps)
	}
	if len(editSteps) != editModelStepCount {
		t.Fatalf("edit steps = %d, want %d: %q", len(editSteps), editModelStepCount, editSteps)
	}
	// Both forms ask the same first six questions, in the same order.
	for step := 0; step < addModelStepCount; step++ {
		if editSteps[step] != addSteps[step] {
			t.Errorf("step %d: edit label %q, want the add label %q", step, editSteps[step], addSteps[step])
		}
	}
	wantReasoningLabels := map[int]string{
		editModelReasoningEnableStep:    "Reasoning (yes/no)",
		editModelReasoningEffortStep:    "Reasoning effort",
		editModelReasoningMaxTokensStep: "Reasoning max tokens",
		editModelReasoningExcludeStep:   "Reasoning exclude (yes/no/default)",
		editModelReasoningSummaryStep:   "Reasoning summary",
	}
	for step, want := range wantReasoningLabels {
		if editSteps[step] != want {
			t.Errorf("edit step %d label = %q, want %q", step, editSteps[step], want)
		}
	}
	// The catalog "(thinking: …)" decoration stays on the model-name label of
	// BOTH modes.
	m.addModelCatalogThink = "low/medium/high"
	m.modalMode = ModalAddModel
	addSteps = m.formStepNames()
	if !strings.Contains(addSteps[addModelModelNameStep], "(thinking: low/medium/high)") {
		t.Errorf("add model-name label = %q, want the thinking decoration", addSteps[addModelModelNameStep])
	}
	m.modalMode = ModalEditModel
	editSteps = m.formStepNames()
	if !strings.Contains(editSteps[addModelModelNameStep], "(thinking: low/medium/high)") {
		t.Errorf("edit model-name label = %q, want the thinking decoration", editSteps[addModelModelNameStep])
	}
}

// TestModelFormShapeMatchesConstants guards the invariant both model forms rest
// on: formValues, formStepNames() and the step-count constants must describe the
// same steps for EACH mode. If they drift, the ENTER handler either saves too
// early or reads a value that was never collected.
func TestModelFormShapeMatchesConstants(t *testing.T) {
	tests := []struct {
		mode      modalType
		wantSteps int
	}{
		{ModalAddModel, addModelStepCount},
		{ModalEditModel, editModelStepCount},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("mode_%d", tc.mode), func(t *testing.T) {
			m := newEditModelTestModel(t)
			if tc.mode == ModalEditModel {
				m = openProviderDetail(m)
				m = openEditForm(t, m, "gpt-4o")
			} else {
				m.resetModal(ModalAddModel)
				// The add flow saves into the provider the detail view is on;
				// the "+ Add model" action row keeps it set.
				m.providerSelectedName = "test-provider"
				m.formValues = make([]string, addModelStepCount)
				m.textInput.SetValue("alias")
			}

			steps := m.formStepNames()
			if len(steps) != tc.wantSteps {
				t.Fatalf("formStepNames() has %d steps, want %d: %q", len(steps), tc.wantSteps, steps)
			}
			if len(m.formValues) != tc.wantSteps {
				t.Fatalf("formValues has %d entries, want %d: %q", len(m.formValues), tc.wantSteps, m.formValues)
			}
			// Every step must be reachable by pressing ENTER, and the last one
			// saves (the add form closes the modal, the edit form returns to the
			// provider detail).
			for step := 0; step < tc.wantSteps-1; step++ {
				if step == 0 && tc.mode == ModalAddModel {
					m.textInput.SetValue("shape-alias")
				}
				if step == addModelModelNameStep && tc.mode == ModalAddModel {
					m.textInput.SetValue("shape-model-001")
				}
				if step == addModelVisionStep && tc.mode == ModalAddModel {
					// The vision step carries a required yes/no answer.
					m.textInput.SetValue("no")
				}
				if m.formStepIndex != step {
					t.Fatalf("at step %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
				}
				m = sendKeys(m, "\r")
				if m.modalMode != tc.mode {
					t.Fatalf("ENTER on step %d closed the form early (formError = %q)", step, m.formError)
				}
			}
			if m.formStepIndex != tc.wantSteps-1 {
				t.Fatalf("formStepIndex = %d, want the last step (%d)", m.formStepIndex, tc.wantSteps-1)
			}
			m = sendKeys(m, "\r")
			if tc.mode == ModalEditModel {
				if m.modalMode != ModalProviderDetail {
					t.Fatalf("the last ENTER did not save: modal = %v (formError = %q)", m.modalMode, m.formError)
				}
			} else if m.modalMode != ModalNone {
				t.Fatalf("the add form must close on save, modal = %v (formError = %q)", m.modalMode, m.formError)
			}
		})
	}
}

// TestEditModelEscFromEveryStepReturnsToDetail verifies ESC from ANY of the 11
// steps — the reasoning steps included — discards the draft and lands back on
// the provider detail with the edited row selected and the config untouched.
func TestEditModelEscFromEveryStepReturnsToDetail(t *testing.T) {
	for step := 0; step < editModelStepCount; step++ {
		t.Run(fmt.Sprintf("step_%d", step), func(t *testing.T) {
			m := newEditModelTestModel(t)
			before := savedModel(t, m, "gpt-4o")
			m = openProviderDetail(m)
			m = openEditForm(t, m, "gpt-4o")

			// Walk to the step, then type a change that must be discarded.
			if step > 0 {
				m = walkSteps(t, m, step-1, nil)
			}
			if m.formStepIndex != step {
				t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
			}
			m.textInput.SetValue("bogus")

			// The model-name step may show catalog suggestions, and ESC hides
			// those first (documented behavior: the picker is dismissed before
			// the form); a second ESC then leaves the form.
			pickerActive := m.addModelCatalogActive
			m = sendKeys(m, "\x1b")
			if pickerActive && m.modalMode == ModalEditModel {
				m = sendKeys(m, "\x1b")
			}
			if m.modalMode != ModalProviderDetail {
				t.Fatalf("modal = %v, want ModalProviderDetail after ESC", m.modalMode)
			}
			if m.formError != "" {
				t.Errorf("formError = %q, want it cleared on ESC", m.formError)
			}
			if alias, ok := m.providerDetailRows[m.modalSelectedIdx]; !ok || alias != "gpt-4o" {
				t.Errorf("detail cursor on idx %d (%q), want gpt-4o", m.modalSelectedIdx, alias)
			}

			after := savedModel(t, m, "gpt-4o")
			assertReasoningEqual(t, after.Reasoning, before.Reasoning)
			if after.Model != before.Model || after.ContextWindow != before.ContextWindow {
				t.Errorf("stored model changed by open+ESC: %+v", after)
			}
		})
	}
}

// TestEditModelReasoningHintsAndPlaceholders verifies the new steps are usable:
// every reasoning step carries a placeholder, and the optional ones show the
// "default" hint (steps 7, 9, 10), max tokens its own (8).
func TestEditModelReasoningHintsAndPlaceholders(t *testing.T) {
	m := newEditModelTestModel(t)
	m = openProviderDetail(m)
	m = openEditForm(t, m, "gpt-4o")

	wantHints := map[int]string{
		editModelReasoningEffortStep:    "tui.editModelReasoningDefaultHint",
		editModelReasoningMaxTokensStep: "tui.editModelReasoningTokensHint",
		editModelReasoningExcludeStep:   "tui.editModelReasoningDefaultHint",
		editModelReasoningSummaryStep:   "tui.editModelReasoningDefaultHint",
	}

	for step := editModelReasoningEnableStep; step < editModelStepCount; step++ {
		if step > 0 {
			m = walkSteps(t, m, step-1, nil)
		}
		if m.formStepIndex != step {
			t.Fatalf("step = %d, want %d (formError = %q)", m.formStepIndex, step, m.formError)
		}
		if m.textInput.Placeholder == "" {
			t.Errorf("step %d has no placeholder", step)
		}
		hint := m.renderFormStepHint(false)
		wantKey, wantHinted := wantHints[step]
		if wantHinted {
			if hint == "" {
				t.Errorf("step %d: no hint rendered, want %s", step, wantKey)
			}
			if !strings.Contains(hint, i18n.T(wantKey)) {
				t.Errorf("step %d hint = %q, want it to contain %q", step, hint, i18n.T(wantKey))
			}
			continue
		}
		if hint != "" {
			t.Errorf("step %d: hint = %q, want none", step, hint)
		}
	}
}

// ── assertion helpers ──

// assertReasoningEqual compares two reasoning configs field by field, failing on
// both the value and the presence of every pointer.
func assertReasoningEqual(t *testing.T, got, want *config.ReasoningConfig) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("Reasoning = %+v, want %+v", got, want)
		}
		return
	}
	if got.Enable != want.Enable {
		t.Errorf("Reasoning.Enable = %v, want %v", got.Enable, want.Enable)
	}
	assertPtrEqual(t, "Effort", got.Effort, want.Effort)
	assertPtrEqual(t, "MaxTokens", got.MaxTokens, want.MaxTokens)
	assertPtrEqual(t, "Exclude", got.Exclude, want.Exclude)
	assertPtrEqual(t, "Summary", got.Summary, want.Summary)
}

// assertPtrEqual compares two optional values, treating "unset" as a value.
func assertPtrEqual[T comparable](t *testing.T, field string, got, want *T) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Errorf("Reasoning.%s = %v, want %v", field, deref(got), deref(want))
		}
		return
	}
	if *got != *want {
		t.Errorf("Reasoning.%s = %v, want %v", field, *got, *want)
	}
}

// deref renders an optional value for failure messages (nil reads as "<unset>").
func deref[T any](p *T) any {
	if p == nil {
		return "<unset>"
	}
	return *p
}

// ptrString / ptrInt / ptrBool build the expected pointers of a test table.
func ptrString(v string) *string { return &v }
func ptrInt(v int) *int          { return &v }
func ptrBool(v bool) *bool       { return &v }
