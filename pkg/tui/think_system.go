package tui

import (
	"github.com/xilistudios/lele/pkg/config"
)

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
