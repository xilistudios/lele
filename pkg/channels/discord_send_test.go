package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/xilistudios/lele/pkg/config"
)

// Tests for SURV-05: sendChunk must not leak a goroutine per timed-out send.
// The old implementation fired the discordgo call in a fire-and-forget
// goroutine and returned on timeout, leaving that goroutine blocked in the
// HTTP call until Discord answered — result discarded. The fix threads the
// deadline INTO the request via discordgo.WithContext, so the HTTP call
// itself is canceled and the caller's goroutine is the only one involved.

// newDiscordSendFixture spins up an httptest server masquerading as the
// Discord API (discordgo.EndpointDiscord is a mutable package var), and a
// DiscordChannel wired to it. Real JSON is returned so discordgo parses the
// response normally on the happy path.
func newDiscordSendFixture(t *testing.T, handler http.HandlerFunc) (*DiscordChannel, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Repoint discordgo at the fake API (old values restored for other
	// tests). EndpointChannels and friends are computed at package init from
	// EndpointAPI, so each leaf actually used by sends must be repointed
	// individually — mutating the parents alone sends traffic to real Discord.
	oldEndpoint := discordgo.EndpointDiscord
	oldChannels := discordgo.EndpointChannels
	fakeAPI := srv.URL + "/api/v" + discordgo.APIVersion + "/"
	discordgo.EndpointDiscord = srv.URL + "/"
	discordgo.EndpointAPI = fakeAPI
	discordgo.EndpointChannels = fakeAPI + "channels/"
	t.Cleanup(func() {
		discordgo.EndpointDiscord = oldEndpoint
		discordgo.EndpointAPI = oldEndpoint + "api/v" + discordgo.APIVersion + "/"
		discordgo.EndpointChannels = oldChannels
	})

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}

	ch := &DiscordChannel{
		BaseChannel: NewBaseChannel("discord", config.DiscordConfig{}, nil, nil),
		session:     session,
	}
	return ch, srv
}

func discordMessageJSON(t *testing.T) []byte {
	t.Helper()
	payload := map[string]interface{}{
		"id":               "9999",
		"channel_id":       "123",
		"content":          "ok",
		"author":           map[string]interface{}{"id": "1", "username": "bot", "discriminator": "0001"},
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
		"tts":              false,
		"mention_everyone": false,
		"mentions":         []interface{}{},
		"attachments":      []interface{}{},
		"embeds":           []interface{}{},
		"pinned":           false,
		"type":             0,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return b
}

// TestSendChunk_Success verifies the happy path still works through
// ChannelMessageSendComplex with the context option attached.
func TestSendChunk_Success(t *testing.T) {
	var hits atomic.Int32
	ch, _ := newDiscordSendFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(discordMessageJSON(t))
	})

	if err := ch.sendChunk(context.Background(), "123", "hello"); err != nil {
		t.Fatalf("sendChunk: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
}

// TestSendChunk_TimeoutCancelsRequest is the core SURV-05 assertion: when the
// server is slower than the deadline, sendChunk returns at the deadline AND
// the server observes its request context being canceled — proof the cancel
// propagated into the in-flight HTTP call instead of abandoning it.
func TestSendChunk_TimeoutCancelsRequest(t *testing.T) {
	requestCanceled := make(chan struct{}, 1)
	var hits atomic.Int32

	ch, _ := newDiscordSendFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Drain the JSON body first: while body bytes remain unread the
		// server cannot detect the client disconnect, and ctx.Done() never
		// fires — that masked the very signal this test asserts on.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done() // hang until the client gives up
		requestCanceled <- struct{}{}
	})

	old := discordSendTimeout
	discordSendTimeout = 150 * time.Millisecond
	t.Cleanup(func() { discordSendTimeout = old })

	start := time.Now()
	err := ch.sendChunk(context.Background(), "123", "hello")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("sendChunk took %v; timeout did not bound the call", elapsed)
	}
	select {
	case <-requestCanceled:
		// Server saw the cancellation: the deadline reached the HTTP layer.
	case <-time.After(2 * time.Second):
		t.Fatalf("server never observed request cancellation; ctx not propagated to the HTTP call")
	}
}

// TestSendChunk_CallerCancelPropagates verifies an already-canceled caller
// context aborts the send immediately — the ctx parameter participates in
// the deadline chain.
func TestSendChunk_CallerCancelPropagates(t *testing.T) {
	var hits atomic.Int32
	ch, _ := newDiscordSendFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := ch.sendChunk(ctx, "123", "hello"); err == nil {
		t.Fatalf("expected error for canceled caller ctx, got nil")
	}
	if hits.Load() != 0 {
		t.Fatalf("request reached server despite canceled ctx")
	}
}
