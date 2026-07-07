package i18n

import (
	"fmt"
	"strings"
)

// Formatted returns a formatted translation with arguments.
// Example: Formatted("tui.welcome.user", "John") -> "¡Bienvenido, John!"
func Formatted(key string, args ...interface{}) string {
	return fmt.Sprintf(T(key), args...)
}

// Plural returns the appropriate plural form based on count.
// This is a simple implementation. For complex pluralization rules,
// consider using golang.org/x/text/message.
func Plural(singularKey, pluralKey string, count int) string {
	if count == 1 {
		return T(singularKey)
	}
	return T(pluralKey)
}

// JoinTranslations joins multiple translation keys with a separator.
func JoinTranslations(sep string, keys ...string) string {
	translations := make([]string, len(keys))
	for i, key := range keys {
		translations[i] = T(key)
	}
	return strings.Join(translations, sep)
}

// StatusWithLabel returns a status string formatted as "Label: Status"
func StatusWithLabel(labelKey, statusKey string) string {
	return fmt.Sprintf("%s: %s", T(labelKey), T(statusKey))
}
