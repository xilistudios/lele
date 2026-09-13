package channels

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// newLINEForTest builds a LINEChannel with only what webhookHandler touches
// before signature verification (config.ChannelSecret for HMAC, nothing else).
func newLINEForTest() *LINEChannel {
	return &LINEChannel{
		BaseChannel: NewBaseChannel("line", config.LINEConfig{}, nil, nil),
		config:      config.LINEConfig{ChannelSecret: "test-secret"},
	}
}

// NEW-01: the webhook body must be capped BEFORE it is read. The signature is
// verified after the read, so an unauthenticated caller sending a huge body
// used to be able to force unbounded memory allocation.
func TestLINEWebhook_BodySizeLimit(t *testing.T) {
	c := newLINEForTest()

	huge := bytes.Repeat([]byte("a"), (1<<20)+4096) // just over the 1MB cap
	req := httptest.NewRequest(http.MethodPost, "/webhook/line", bytes.NewReader(huge))
	rec := httptest.NewRecorder()

	c.webhookHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: got status %d, want %d (MaxBytesReader must reject)", rec.Code, http.StatusBadRequest)
	}
}

// Regression guard: a normal-size body with a bad signature must still reach
// the signature check (403), i.e. the cap did not swallow the valid path.
func TestLINEWebhook_SmallBodyStillSignatureChecked(t *testing.T) {
	c := newLINEForTest()

	body := `{"events":[]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/line", strings.NewReader(body))
	req.Header.Set("X-Line-Signature", "bogus")
	rec := httptest.NewRecorder()

	c.webhookHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("small body w/ bad signature: got status %d, want %d", rec.Code, http.StatusForbidden)
	}
}
