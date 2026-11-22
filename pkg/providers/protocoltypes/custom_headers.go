package protocoltypes

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const SessionHeaderPlaceholder = "{{session}}"

const (
	placeholderOpen  = "{{"
	placeholderClose = "}}"
)

const sessionPlaceholderName = "session"

var envVarTokenRegex = regexp.MustCompile(`^ENV_[A-Za-z_][A-Za-z0-9_]*(:[^}]*)?$`)

func isEnvPlaceholderName(inner string) bool {
	return envVarTokenRegex.MatchString(inner)
}

func ExpandHeaderValue(value, sessionID string) string {
	return strings.ReplaceAll(value, SessionHeaderPlaceholder, sessionID)
}

var forbiddenHeaderNames = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true,
	"connection": true, "keep-alive": true, "proxy-connection": true,
	"upgrade": true, "te": true, "trailer": true, "content-type": true,
}

func ValidateCustomHeaders(headers map[string]string) error {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if !isHeaderName(name) {
			return fmt.Errorf("custom header %q: invalid name: must be an RFC 7230 token", name)
		}
		if forbiddenHeaderNames[strings.ToLower(name)] {
			return fmt.Errorf("custom header %q: is set by the transport and cannot be overridden", name)
		}
		if hasControlChar(headers[name]) {
			return fmt.Errorf("custom header %q: value contains a control character (e.g. CR/LF/NUL)", name)
		}
		if err := checkPlaceholders(name, headers[name]); err != nil {
			return err
		}
	}
	return nil
}

func isHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenByte(name[i]) {
			return false
		}
	}
	return true
}

func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		return true
	}
	return false
}

func hasControlChar(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return true
		}
	}
	return false
}

func checkPlaceholders(name, value string) error {
	for i := 0; i < len(value); {
		if value[i] != '{' || i+1 >= len(value) || value[i+1] != '{' {
			i++
			continue
		}
		rel := strings.Index(value[i+len(placeholderOpen):], placeholderClose)
		if rel < 0 {
			return fmt.Errorf("custom header %q: unterminated placeholder %q", name, value[i:])
		}
		start := i + len(placeholderOpen)
		inner := value[start : start+rel]
		if inner != sessionPlaceholderName && !isEnvPlaceholderName(inner) {
			return fmt.Errorf("custom header %q: unknown placeholder %q (supported: %s, {{ENV_NAME}}, {{ENV_NAME:default}})",
				name, placeholderOpen+inner+placeholderClose, SessionHeaderPlaceholder)
		}
		i = start + rel + len(placeholderClose)
	}
	return nil
}
