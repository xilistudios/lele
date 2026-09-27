package tui

import (
	"github.com/xilistudios/lele/pkg/config"
)

// thinkSystemFieldValue validates the free-text think-system input of a model
// form step and returns the canonical value to store in formValues. Empty
// input means the default and is stored as "auto"; addModelToProvider maps
// "auto" back to an unset ThinkingType so the saved config stays clean.
func thinkSystemFieldValue(val string) (string, error) {
	tt, err := normalizeThinkSystemInput(val)
	if err != nil {
		return "", err
	}
	if tt == "" {
		return config.ThinkingTypeAuto, nil
	}
	return tt, nil
}
