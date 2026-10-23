// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import (
	"regexp"
	"testing"
)

func TestExpandHeaderValue(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		sessionID string
		want      string
	}{
		{
			name:      "no placeholder is untouched",
			value:     "static-value",
			sessionID: "abc",
			want:      "static-value",
		},
		{
			name:      "one occurrence",
			value:     "prefix-{{session}}-suffix",
			sessionID: "abc",
			want:      "prefix-abc-suffix",
		},
		{
			name:      "two occurrences",
			value:     "{{session}}/{{session}}",
			sessionID: "abc",
			want:      "abc/abc",
		},
		{
			name:      "value is exactly the placeholder",
			value:     SessionHeaderPlaceholder,
			sessionID: "abc",
			want:      "abc",
		},
		{
			name:      "session id needs no escaping",
			value:     "{{session}}",
			sessionID: UnkeyedSessionBucket,
			want:      UnkeyedSessionBucket,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandHeaderValue(tt.value, tt.sessionID); got != tt.want {
				t.Fatalf("ExpandHeaderValue(%q, %q) = %q, want %q", tt.value, tt.sessionID, got, tt.want)
			}
		})
	}
}

func TestValidateCustomHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		wantErr string // "" means the map must be accepted
	}{
		{
			name:    "nil map",
			headers: nil,
			wantErr: "",
		},
		{
			name:    "empty map",
			headers: map[string]string{},
			wantErr: "",
		},
		{
			name: "valid map with the session template and an auth override",
			headers: map[string]string{
				"x-opencode-session": "{{session}}",
				"Authorization":      "Bearer x",
				"x-tenant":           "",
				"x-spaced":           "  padded value  ",
			},
			wantErr: "",
		},
		{
			name:    "name with a space",
			headers: map[string]string{"bad name": "v"},
			wantErr: `custom header "bad name": invalid name: must be an RFC 7230 token`,
		},
		{
			name:    "name with a colon",
			headers: map[string]string{"x:y": "v"},
			wantErr: `custom header "x:y": invalid name: must be an RFC 7230 token`,
		},
		{
			name:    "empty name",
			headers: map[string]string{"": "v"},
			wantErr: `custom header "": invalid name: must be an RFC 7230 token`,
		},
		{
			name:    "CRLF injection in the value",
			headers: map[string]string{"x-a": "a\r\nX-Evil: 1"},
			wantErr: `custom header "x-a": value contains a control character (CR/LF/NUL)`,
		},
		{
			name:    "NUL in the value",
			headers: map[string]string{"x-a": "a\x00b"},
			wantErr: `custom header "x-a": value contains a control character (CR/LF/NUL)`,
		},
		{
			name:    "DEL in the value",
			headers: map[string]string{"x-a": "a\x7fb"},
			wantErr: `custom header "x-a": value contains a control character (CR/LF/NUL)`,
		},
		{
			// The control-char check runs on the RAW value: transports expand
			// and send it untrimmed, so a trailing CR must fail at load with
			// the header named, not later inside http.Client.
			name:    "trailing CR in the raw value",
			headers: map[string]string{"x-a": "v\r"},
			wantErr: `custom header "x-a": value contains a control character (CR/LF/NUL)`,
		},
		{
			name:    "trailing LF in the raw value",
			headers: map[string]string{"x-a": "v\n"},
			wantErr: `custom header "x-a": value contains a control character (CR/LF/NUL)`,
		},
		{
			name:    "surrounding spaces are legal and stay valid",
			headers: map[string]string{"x-a": " v "},
			wantErr: "",
		},
		{
			name:    "empty raw value stays allowed",
			headers: map[string]string{"x-a": ""},
			wantErr: "",
		},
		{
			name:    "forbidden host",
			headers: map[string]string{"Host": "evil.example"},
			wantErr: `custom header "Host": is set by the transport and cannot be overridden`,
		},
		{
			name:    "forbidden content-type is case-insensitive",
			headers: map[string]string{"content-TYPE": "text/plain"},
			wantErr: `custom header "content-TYPE": is set by the transport and cannot be overridden`,
		},
		{
			name:    "forbidden transfer-encoding",
			headers: map[string]string{"Transfer-Encoding": "chunked"},
			wantErr: `custom header "Transfer-Encoding": is set by the transport and cannot be overridden`,
		},
		{
			name:    "unknown placeholder",
			headers: map[string]string{"x-a": "{{sesssion}}"},
			wantErr: `custom header "x-a": unknown placeholder "{{sesssion}}" (only {{session}} is supported)`,
		},
		{
			name:    "unterminated placeholder",
			headers: map[string]string{"x-a": "{{session"},
			wantErr: `custom header "x-a": unterminated placeholder "{{session"`,
		},
		{
			name:    "literal braces are not a placeholder",
			headers: map[string]string{"x-a": `{"a}b"}`},
			wantErr: "",
		},
		{
			name: "several problems report the alphabetically first header",
			// Map iteration order is random; sorting is what makes the message
			// (and therefore this assertion) reproducible.
			headers: map[string]string{"z-bad name": "v", "a-bad name": "v"},
			wantErr: `custom header "a-bad name": invalid name: must be an RFC 7230 token`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCustomHeaders(tt.headers)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateCustomHeaders(%v) = %v, want no error", tt.headers, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateCustomHeaders(%v) = nil, want error %q", tt.headers, tt.wantErr)
			}
			if got := err.Error(); got != tt.wantErr {
				t.Fatalf("ValidateCustomHeaders() error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

func TestWireSessionForHeaders(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		want    string
	}{
		{
			name:    "stamped id is returned",
			options: map[string]interface{}{OptWireSessionID: "abc123"},
			want:    "abc123",
		},
		{
			name:    "unstamped call falls back to the unkeyed bucket",
			options: map[string]interface{}{"other": "x"},
			want:    UnkeyedSessionBucket,
		},
		{
			name:    "non-string value falls back to the unkeyed bucket",
			options: map[string]interface{}{OptWireSessionID: 42},
			want:    UnkeyedSessionBucket,
		},
		{
			name:    "nil options fall back to the unkeyed bucket",
			options: nil,
			want:    UnkeyedSessionBucket,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WireSessionForHeaders(tt.options); got != tt.want {
				t.Fatalf("WireSessionForHeaders(%v) = %q, want %q", tt.options, got, tt.want)
			}
		})
	}
}

// TestWireSessionIDUnkeyedBucket pins the shape the header scheme promises: a
// blank key is never "no id" but the shared bucket, and every id is 64 hex
// chars. pkg/providers/wire_session_test.go covers the rest of the scheme.
func TestWireSessionIDUnkeyedBucket(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, key := range []string{"", "   "} {
		if got := WireSessionID(key); got != UnkeyedSessionBucket {
			t.Fatalf("WireSessionID(%q) = %q, want the unkeyed bucket %q", key, got, UnkeyedSessionBucket)
		}
	}
	if !hex64.MatchString(UnkeyedSessionBucket) {
		t.Fatalf("UnkeyedSessionBucket = %q, want 64 lowercase hex chars", UnkeyedSessionBucket)
	}
	if !hex64.MatchString(WireSessionID("native:chat:1")) {
		t.Fatalf("WireSessionID() = %q, want 64 lowercase hex chars", WireSessionID("native:chat:1"))
	}
}
