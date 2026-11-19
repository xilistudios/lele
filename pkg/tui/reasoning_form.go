package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
)

// reasoningFormDefault is the answer that CLEARS an optional reasoning
// override in the edit-model form. It exists because a form step needs a way to
// say "unset" that is not blank: blank accepts the pre-filled value (Enter
// through a step), so the explicit escape hatch has to be a word the user types.
const reasoningFormDefault = "default"

// reasoningFieldValue returns the form value of a reasoning step, or "" when
// the slice is shorter than the form (a defensive read so a malformed form
// cannot panic; the shape invariant test guards the length).
func reasoningFieldValue(vals []string, step int) string {
	if step < 0 || step >= len(vals) {
		return ""
	}
	return vals[step]
}

// normalizeReasoningEnable validates the "Reasoning (yes/no)" step: yes enables
// the model's reasoning mode, no disables it.
func normalizeReasoningEnable(val string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("must be yes or no")
	}
}

// normalizeReasoningEffort resolves the "Reasoning effort" step. Blank and
// "default" both mean "no override" (nil); low/medium/high are stored
// lowercased. "off" is deliberately rejected: it is a valid session thinking
// level (config.NormalizeThinkingLevel) but NOT a valid per-model reasoning
// effort (config.ReasoningConfig.Validate).
func normalizeReasoningEffort(val string) (*string, error) {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "", reasoningFormDefault:
		return nil, nil
	case "low", "medium", "high":
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid reasoning effort: %q (must be low, medium, high or default)", val)
	}
}

// normalizeReasoningMaxTokens resolves the "Reasoning max tokens" step. Blank
// and "default" mean "no override" (nil); anything else must be a positive
// integer because config.ReasoningConfig.Validate rejects < 1.
func normalizeReasoningMaxTokens(val string) (*int, error) {
	v := strings.TrimSpace(val)
	switch strings.ToLower(v) {
	case "", reasoningFormDefault:
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("invalid reasoning max tokens: %q (must be a positive integer)", val)
	}
	return &n, nil
}

// normalizeReasoningExclude resolves the "Reasoning exclude" step: yes/no set
// the override, blank and "default" clear it (nil = provider default).
func normalizeReasoningExclude(val string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "", reasoningFormDefault:
		return nil, nil
	case "yes":
		yes := true
		return &yes, nil
	case "no":
		no := false
		return &no, nil
	default:
		return nil, fmt.Errorf("invalid reasoning exclude: %q (must be yes, no or default)", val)
	}
}

// normalizeReasoningSummary resolves the "Reasoning summary" step. Blank and
// "default" mean "no override" (nil); auto/detailed/concise are stored
// lowercased, matching config.ReasoningConfig.Validate.
func normalizeReasoningSummary(val string) (*string, error) {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "", reasoningFormDefault:
		return nil, nil
	case "auto", "detailed", "concise":
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid reasoning summary: %q (must be auto, detailed, concise or default)", val)
	}
}

// reasoningFromFormValues turns the five reasoning steps of the edit-model form
// (editModelReasoningEnableStep … editModelReasoningSummaryStep, in that order)
// into the config.ReasoningConfig to store on the model.
//
// Rules:
//   - step 6 answers yes/no and is the ReasoningConfig.Enable flag;
//   - steps 7-10 are optional overrides: blank or "default" leaves the pointer
//     nil, a real answer is stored (lowercased where the config expects an
//     enum, trimmed everywhere);
//   - when reasoning is disabled AND no override was given, the result is nil
//     so the saved model keeps a clean, omitempty-free JSON (a struct full of
//     zero values would write "enable": false for no reason);
//   - the result is validated before it is returned, so the caller surfaces one
//     error message instead of saving a config the loader would reject.
func reasoningFromFormValues(vals []string) (*config.ReasoningConfig, error) {
	enable, err := normalizeReasoningEnable(reasoningFieldValue(vals, editModelReasoningEnableStep))
	if err != nil {
		return nil, err
	}
	effort, err := normalizeReasoningEffort(reasoningFieldValue(vals, editModelReasoningEffortStep))
	if err != nil {
		return nil, err
	}
	maxTokens, err := normalizeReasoningMaxTokens(reasoningFieldValue(vals, editModelReasoningMaxTokensStep))
	if err != nil {
		return nil, err
	}
	exclude, err := normalizeReasoningExclude(reasoningFieldValue(vals, editModelReasoningExcludeStep))
	if err != nil {
		return nil, err
	}
	summary, err := normalizeReasoningSummary(reasoningFieldValue(vals, editModelReasoningSummaryStep))
	if err != nil {
		return nil, err
	}

	if !enable && effort == nil && maxTokens == nil && exclude == nil && summary == nil {
		return nil, nil
	}

	rc := &config.ReasoningConfig{
		Enable:    enable,
		Effort:    effort,
		MaxTokens: maxTokens,
		Exclude:   exclude,
		Summary:   summary,
	}
	if err := rc.Validate(); err != nil {
		return nil, err
	}
	return rc, nil
}
