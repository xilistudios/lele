package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// A failed LLM turn is persisted as an assistant message carrying ErrorDetail
// (UI-only) with ExcludeFromContext=true. GET
// /api/v1/chat/sessions/{sessionKey}/history is how the WebUI learns about it
// after a refetch, so these tests drive the real mux and assert on the decoded
// response instead of on the private DTO builder: a regression that filtered
// the failure row out of the endpoint would otherwise pass.

// getChatHistoryRaw performs the authenticated history request and returns the
// status plus the raw body, so the wire contract (key present vs omitted) can be
// asserted on the bytes the WebUI actually receives.
func getChatHistoryRaw(t *testing.T, ts *nativeTestServer, sessionKey string) (int, []byte) {
	t.Helper()

	endpoint := ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape(sessionKey) + "/history"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return resp.StatusCode, body
}

// TestChatHistoryEndpoint_ErrorDetail is the contract the WebUI refetch relies
// on: a persisted failure comes back with its detail and its exclusion flag,
// and an ordinary assistant message still omits the key entirely (omitempty),
// so stored and transmitted JSON stays compatible.
func TestChatHistoryEndpoint_ErrorDetail(t *testing.T) {
	const (
		failureDetail = "LLM call failed after retries: 429 Too Many Requests"
		failureText   = "❌ Error processing message: " + failureDetail
	)

	cases := []struct {
		name string
		// messages is the session history the endpoint serves.
		messages []providers.Message
		// wantErrorDetail / wantExcluded describe the message at wantIndex.
		wantIndex       int
		wantErrorDetail string
		wantExcluded    bool
		// wantKeyPresent is the raw-JSON contract for "error_detail".
		wantKeyPresent bool
	}{
		{
			name: "failed turn is served with its detail and exclusion flag",
			messages: []providers.Message{
				{Role: "user", Content: "hello"},
				{
					Role:               "assistant",
					Content:            failureText,
					ErrorDetail:        failureDetail,
					ExcludeFromContext: true,
				},
			},
			wantIndex:       1,
			wantErrorDetail: failureDetail,
			wantExcluded:    true,
			wantKeyPresent:  true,
		},
		{
			name: "ordinary assistant message omits the key",
			messages: []providers.Message{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
			},
			wantIndex:       1,
			wantErrorDetail: "",
			wantExcluded:    false,
			wantKeyPresent:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newNativeTestServer(t)
			sessionKey := "native:" + ts.clientID + "-errordetail"
			ts.loop.histories[sessionKey] = tc.messages

			status, body := getChatHistoryRaw(t, ts, sessionKey)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want %d (body=%s)", status, http.StatusOK, body)
			}

			var payload ChatHistoryResponse
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode payload: %v (body=%s)", err, body)
			}
			if len(payload.Messages) != len(tc.messages) {
				t.Fatalf("got %d messages, want %d: %s", len(payload.Messages), len(tc.messages), body)
			}

			got := payload.Messages[tc.wantIndex]
			if got.ErrorDetail != tc.wantErrorDetail {
				t.Errorf("messages[%d].error_detail = %q, want %q", tc.wantIndex, got.ErrorDetail, tc.wantErrorDetail)
			}
			if got.ExcludeFromContext != tc.wantExcluded {
				t.Errorf("messages[%d].exclude_from_context = %v, want %v",
					tc.wantIndex, got.ExcludeFromContext, tc.wantExcluded)
			}
			// The legacy plain text must still be there: every non-box channel
			// renders Content, not error_detail.
			if got.Content != tc.messages[tc.wantIndex].Content {
				t.Errorf("messages[%d].content = %q, want %q",
					tc.wantIndex, got.Content, tc.messages[tc.wantIndex].Content)
			}

			if hasKey := strings.Contains(string(body), `"error_detail"`); hasKey != tc.wantKeyPresent {
				t.Errorf("error_detail present in the wire JSON = %v, want %v: %s", hasKey, tc.wantKeyPresent, body)
			}
		})
	}
}
