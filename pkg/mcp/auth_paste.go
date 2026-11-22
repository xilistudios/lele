package mcp

import (
	"errors"
	"net/url"
	"strings"
)

var (
	ErrAuthPasteEmpty        = errors.New("no authorization response was pasted")
	ErrAuthPasteUnrecognized = errors.New("unrecognized authorization response")
	ErrAuthPasteNoCode       = errors.New("the pasted authorization response carries no code")
	ErrAuthPasteDenied       = errors.New("authorization denied by the authorization server")
)

type AuthPasteDeniedError struct{ Code string }

func (e *AuthPasteDeniedError) Error() string {
	if e == nil || e.Code == "" {
		return ErrAuthPasteDenied.Error()
	}
	return ErrAuthPasteDenied.Error() + ": " + e.Code
}

func (e *AuthPasteDeniedError) Unwrap() error { return ErrAuthPasteDenied }

const authPasteMaxInputLen = 8192

const authPasteWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

func authPasteTrim(s string) string { return strings.Trim(s, authPasteWhitespace) }

func authPasteHasWhitespace(s string) bool { return strings.ContainsAny(s, authPasteWhitespace) }

func authPasteSplitTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(authPasteWhitespace, r) })
}

const (
	authPasteParamCode  = "code"
	authPasteParamState = "state"
	authPasteParamError = "error"
)

func ParseAuthPaste(input string) (code, state string, err error) {
	if len(input) > authPasteMaxInputLen {
		return "", "", ErrAuthPasteUnrecognized
	}

	s := authPasteTrim(input)
	if s == "" {
		return "", "", ErrAuthPasteEmpty
	}

	if !authPasteHasWhitespace(s) {
		if strings.Contains(s, "?") {
			values, ok := authPasteParams(s)
			if !ok {
				return "", "", ErrAuthPasteUnrecognized
			}
			return authPasteFromParams(values)
		}
		if strings.Contains(s, "=") {
			values, ok := authPasteParams(s)
			if !ok {
				return "", "", ErrAuthPasteUnrecognized
			}
			if !authPasteHasAny(values, authPasteParamCode, authPasteParamState, authPasteParamError) {
				return "", "", ErrAuthPasteUnrecognized
			}
			return authPasteFromParams(values)
		}
		if !authPasteCodeShapeOK(s) {
			return "", "", ErrAuthPasteUnrecognized
		}
		return s, "", nil
	}

	fields := authPasteSplitTokens(s)
	if len(fields) == 2 &&
		!strings.ContainsAny(fields[0], "?=") && !strings.ContainsAny(fields[1], "?=") &&
		authPasteCodeShapeOK(fields[0]) && authPasteCodeShapeOK(fields[1]) {
		return fields[0], fields[1], nil
	}
	return "", "", ErrAuthPasteUnrecognized
}

func authPasteCodeShapeOK(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == '~', c == '+', c == '/', c == '=':
		default:
			return false
		}
	}
	return true
}

func authPasteHasAny(values url.Values, keys ...string) bool {
	for _, k := range keys {
		if _, ok := values[k]; ok {
			return true
		}
	}
	return false
}

func authPasteParams(s string) (url.Values, bool) {
	query := s
	if i := strings.IndexByte(s, '?'); i >= 0 {
		query = s[i+1:]
	}
	if i := strings.IndexByte(query, '#'); i >= 0 {
		query = query[:i]
	}
	query = strings.TrimPrefix(query, "?")
	if authPasteBytesRefused(query) {
		return nil, false
	}
	values, _ := url.ParseQuery(query)
	if authPasteHasAny(values, authPasteParamCode, authPasteParamError) {
		return values, true
	}
	if i := strings.IndexByte(s, '#'); i >= 0 && i+1 < len(s) {
		fragment := strings.TrimPrefix(s[i+1:], "?")
		if authPasteBytesRefused(fragment) {
			return nil, false
		}
		if values, _ := url.ParseQuery(fragment); len(values) > 0 {
			return values, true
		}
	}
	return values, true
}

func authPasteBytesRefused(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == ';' {
			return true
		}
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !authPasteIsHex(s[i+1]) || !authPasteIsHex(s[i+2]) {
			return true
		}
		if authPasteHexByte(s[i+1], s[i+2]) >= 0x80 {
			return true
		}
		i += 2
	}
	return false
}

func authPasteIsHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func authPasteHexByte(hi, lo byte) byte {
	return authPasteHexNibble(hi)<<4 | authPasteHexNibble(lo)
}

func authPasteHexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func authPasteFromParams(values url.Values) (code, state string, err error) {
	if raw := authPasteTrim(values.Get(authPasteParamError)); raw != "" {
		return "", "", &AuthPasteDeniedError{Code: SanitizeOAuthCode(raw)}
	}
	if raw := authPasteTrim(values.Get(authPasteParamCode)); raw != "" {
		return raw, authPasteTrim(values.Get(authPasteParamState)), nil
	}
	return "", "", ErrAuthPasteNoCode
}

func SanitizeOAuthCode(s string) string {
	return sanitizeOAuthToken(s)
}
