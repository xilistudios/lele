package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
)

const reasoningFormDefault = "default"

func reasoningFieldValue(vals []string, step int) string {
	if step < 0 || step >= len(vals) {
		return ""
	}
	return vals[step]
}

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

func normalizeReasoningEffort(val string) (*string, error) {
	v := strings.ToLower(strings.TrimSpace(val))
	switch v {
	case "", reasoningFormDefault:
		return nil, nil
	case "low", "medium", "high", "xhigh", "max":
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid reasoning effort: %q (must be low, medium, high, xhigh, max or default)", val)
	}
}

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
