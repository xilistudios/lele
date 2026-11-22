package channels

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func newLINEForTest() *LINEChannel {
	return &LINEChannel{
		BaseChannel: NewBaseChannel("line", config.LINEConfig{}, nil, nil),
		config:      config.LINEConfig{ChannelSecret: "test-secret"},
	}
}

func TestLINEWebhook_BodySizeLimit(t *testing.T) {
	c := newLINEForTest()

	huge := bytes.Repeat([]byte("a"), (1<<20)+4096)
	req := httptest.NewRequest(http.MethodPost, "/webhook/line", bytes.NewReader(huge))
	rec := httptest.NewRecorder()

	c.webhookHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: got status %d, want %d (MaxBytesReader must reject)", rec.Code, http.StatusBadRequest)
	}
}

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
