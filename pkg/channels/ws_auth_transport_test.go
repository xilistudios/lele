package channels

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func dialWSWithDialer(t *testing.T, url string, dialer *websocket.Dialer) (*websocket.Conn, string) {
	t.Helper()

	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Dial(%q): %v", url, err)
	}
	t.Cleanup(func() { conn.Close() })

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage(welcome): %v", err)
	}
	if !strings.Contains(string(raw), "welcome") {
		t.Fatalf("first message is not the welcome event: %s", raw)
	}

	selected := ""
	if resp != nil {
		selected = resp.Header.Get("Sec-WebSocket-Protocol")
	}
	return conn, selected
}

func TestWS_SubprotocolAuth(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws"

	dialer := &websocket.Dialer{Subprotocols: []string{"bearer", ts.token}}
	conn, selected := dialWSWithDialer(t, wsURL, dialer)

	if selected != "bearer" {
		t.Fatalf("Sec-WebSocket-Protocol echoed %q, want %q: the browser needs the server to select the protocol it offered", selected, "bearer")
	}
	if got := conn.Subprotocol(); got != "bearer" {
		t.Fatalf("conn.Subprotocol() = %q, want bearer", got)
	}
}

func TestWS_SubprotocolWithBadTokenIsRejected(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws"

	dialer := &websocket.Dialer{Subprotocols: []string{"bearer", "not-a-valid-token"}}
	_, resp, err := dialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("a bad token in the bearer subprotocol authenticated a WebSocket")
	}
	if resp == nil || resp.StatusCode != 401 {
		t.Fatalf("status = %v, want 401", resp)
	}
}

func TestWS_SubprotocolOfferedWithoutTokenIsRejected(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws"

	for _, protocols := range [][]string{{"bearer"}, {"bearer", ""}, {"bearer", "   "}} {
		dialer := &websocket.Dialer{Subprotocols: protocols}
		conn, resp, err := dialer.Dial(wsURL, nil)
		if err == nil {
			conn.Close()
			t.Fatalf("subprotocols %q authenticated a WebSocket with no token", protocols)
		}
		if resp == nil || resp.StatusCode != 401 {
			t.Fatalf("subprotocols %q: status = %v, want 401", protocols, resp)
		}
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("subprotocols %q: reading the rejection body: %v", protocols, readErr)
		}
		if !strings.Contains(string(body), "token_missing") {
			t.Fatalf("subprotocols %q: body = %s, want token_missing: an offer with no credential is a missing token, not an invalid one", protocols, body)
		}
	}
}

func TestWS_QueryTokenDeprecatedWarn(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + ts.token

	var dialErr error
	out := captureLogs(t, func() {
		dialer := &websocket.Dialer{}
		conn, _, err := dialer.Dial(wsURL, nil)
		dialErr = err
		if err == nil {
			conn.Close()
		}
	})
	if dialErr != nil {
		t.Fatalf("the legacy ?token= form is still accepted, got: %v", dialErr)
	}

	if !strings.Contains(out, "query string is deprecated") {
		t.Fatalf("a query-string token must be reported as deprecated, got %q", out)
	}
	if strings.Contains(out, ts.token) {
		t.Fatalf("the deprecation warning must never log the token, got %q", out)
	}
}

func TestWS_NoDeprecationWarnForHeaderAuth(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws"

	header := http.Header{"Authorization": {"Bearer " + ts.token}}

	var dialErr error
	out := captureLogs(t, func() {
		dialer := &websocket.Dialer{}
		conn, resp, err := dialer.Dial(wsURL, header)
		dialErr = err
		if err != nil {
			if resp != nil {
				t.Logf("handshake status %d", resp.StatusCode)
			}
			return
		}
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, raw, readErr := conn.ReadMessage(); readErr != nil {
			t.Logf("welcome read: %v", readErr)
		} else if !strings.Contains(string(raw), "welcome") {
			t.Logf("first message is not the welcome event: %s", raw)
		}
		conn.Close()
	})
	if dialErr != nil {
		t.Fatalf("an Authorization header must authenticate the handshake, got: %v", dialErr)
	}

	if strings.Contains(out, "query string is deprecated") {
		t.Fatalf("a header token must not be reported as deprecated, got %q", out)
	}
}

func TestWS_NoDeprecationWarnForSubprotocolAuth(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws"

	out := captureLogs(t, func() {
		dialer := &websocket.Dialer{Subprotocols: []string{"bearer", ts.token}}
		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("subprotocol dial: %v", err)
		}
		conn.Close()
	})

	if strings.Contains(out, "query string is deprecated") {
		t.Fatalf("a subprotocol token must not be reported as deprecated, got %q", out)
	}
}

func TestWS_BareAuthorizationHeaderTokenStillAuthenticates(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws"

	header := http.Header{"Authorization": {ts.token}}

	dialer := &websocket.Dialer{}
	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("an Authorization header carrying the bare token used to authenticate and must keep working, got status %d: %v", status, err)
	}
	conn.Close()
}

func TestWS_RejectedHeaderTokenFallsBackToQueryToken(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + ts.token

	header := http.Header{"Authorization": {"Bearer not-a-valid-token"}}

	var dialErr error
	out := captureLogs(t, func() {
		dialer := &websocket.Dialer{}
		conn, resp, err := dialer.Dial(wsURL, header)
		dialErr = err
		if err != nil {
			if resp != nil {
				t.Logf("handshake status %d", resp.StatusCode)
			}
			return
		}
		conn.Close()
	})
	if dialErr != nil {
		t.Fatalf("a stale Authorization header plus a valid ?token= used to authenticate and must keep working, got: %v", dialErr)
	}
	if !strings.Contains(out, "query string is deprecated") {
		t.Fatalf("a handshake that ends up using the query token must still report the deprecation, got %q", out)
	}
}

func TestWS_RejectedSubprotocolTokenFallsBackToQueryToken(t *testing.T) {
	ts := newNativeTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + ts.token

	dialer := &websocket.Dialer{Subprotocols: []string{"bearer", "not-a-valid-token"}}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("a stale subprotocol token plus a valid ?token= must authenticate, got status %d: %v", status, err)
	}
	conn.Close()
}
