package i18n

import (
	"fmt"
	"strings"
)

func Formatted(key string, args ...interface{}) string {
	return fmt.Sprintf(T(key), args...)
}

func Plural(singularKey, pluralKey string, count int) string {
	if count == 1 {
		return T(singularKey)
	}
	return T(pluralKey)
}

func JoinTranslations(sep string, keys ...string) string {
	translations := make([]string, len(keys))
	for i, key := range keys {
		translations[i] = T(key)
	}
	return strings.Join(translations, sep)
}

func StatusWithLabel(labelKey, statusKey string) string {
	return fmt.Sprintf("%s: %s", T(labelKey), T(statusKey))
}
