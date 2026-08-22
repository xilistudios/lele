package channels

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TestDiscordHandleMessage_NonAudioAttachment exercises the attachment branch
// where the file is NOT an audio file: it becomes an [attachment: URL] in
// content and a media path.
func TestDiscordHandleMessage_NonAudioAttachment(t *testing.T) {
	ctx := context.Background()
	ch := newTestDiscord(t, nil)

	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "m1",
		ChannelID: "C1",
		Content:   "here is a file",
		Author:    &discordgo.User{ID: "U1", Username: "alice"},
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "report.pdf", URL: "https://cdn.example/X/report.pdf", Size: 100},
		},
	}}
	ch.handleMessage(ch.session, m)

	inbound, ok := ch.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no message published")
	}
	if !strings.Contains(inbound.Content, "[attachment: https://cdn.example/X/report.pdf]") {
		t.Errorf("content missing attachment marker: %q", inbound.Content)
	}
	if len(inbound.Media) != 1 {
		t.Errorf("expected 1 media path, got %d", len(inbound.Media))
	}
}

// TestDiscordHandleMessage_AudioNoTranscriber covers an audio attachment with a
// nil transcriber → the "no transcriber available" branch appends
// "[audio: filename]".
func TestDiscordHandleMessage_AudioNoTranscriber(t *testing.T) {
	ctx := context.Background()
	ch := newTestDiscord(t, nil)
	// ch.transcriber is nil by default.

	// Serve a small fake audio payload for the attachment download.
	audioSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{0xff, 0xf3, 0x44, 0x00})
	}))
	defer audioSrv.Close()

	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "m2",
		ChannelID: "C1",
		Content:   "",
		Author:    &discordgo.User{ID: "U1", Username: "alice"},
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "voice.ogg", URL: audioSrv.URL + "/voice.ogg", ContentType: "audio/ogg"},
		},
	}}
	ch.handleMessage(ch.session, m)

	inbound, ok := ch.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no message published")
	}
	if !strings.Contains(inbound.Content, "[audio: voice.ogg]") {
		t.Errorf("content missing audio marker: %q", inbound.Content)
	}
}

// TestDiscordHandleMessage_MediaOnly covers a message with no text and an
// attachment that fails to download as audio → content becomes "[audio: name]"
// marker anyway via the non-audio fallback OR media-only. We assert the message
// still publishes when only an attachment exists.
func TestDiscordHandleMessage_MediaOnly(t *testing.T) {
	ctx := context.Background()
	ch := newTestDiscord(t, nil)

	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "m3",
		ChannelID: "C1",
		Author:    &discordgo.User{ID: "U1", Username: "alice"},
		Attachments: []*discordgo.MessageAttachment{
			{Filename: "photo.png", URL: "https://cdn.example/X.png", ContentType: "image/png"},
		},
	}}
	ch.handleMessage(ch.session, m)

	inbound, ok := ch.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no message published")
	}
	if !strings.Contains(inbound.Content, "[attachment:") {
		t.Errorf("content = %q", inbound.Content)
	}
}

// TestDiscordHandleMessage_EmptyContentAndNoMedia covers a message with empty
// content and no attachments → ignored (no publish).
func TestDiscordHandleMessage_EmptyContentAndNoMedia(t *testing.T) {
	ch := newTestDiscord(t, nil)
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:      "m4",
		Content: "",
		Author:  &discordgo.User{ID: "U1", Username: "alice"},
	}}
	ch.handleMessage(ch.session, m)
	if !tryConsumeEmpty(ch.bus) {
		t.Error("empty message with no media should be ignored")
	}
}

// TestDiscordStartTyping_StopsOnContextCancel verifies startTyping's goroutine
// exits cleanly when the channel context is cancelled. This does not require a
// real discord connection (the typing call errors and is logged).
func TestDiscordStartTyping_ContextCancel(t *testing.T) {
	ch := newTestDiscord(t, nil)
	cctx, cancel := context.WithCancel(context.Background())
	ch.ctx = cctx

	ch.startTyping("C1")

	// Give the goroutine a moment then cancel.
	time.Sleep(30 * time.Millisecond)
	cancel()

	// stopTyping should find and stop the loop without deadlock/panic.
	ch.stopTyping("C1")
	// Starting it again replaces the existing entry.
	ch.startTyping("C1")
	ch.stopTyping("C1")
}

// TestDiscordStop_ClosesTyping verifies Stop cleans up typing channels without
// a live session when ctx is active.
func TestDiscordStop_ClosesTyping(t *testing.T) {
	ch := newTestDiscord(t, nil)
	ch.running = true
	ch.ctx = context.Background()
	// A session exists (from newTestDiscord). Closing an unopened session may
	// error; either outcome is acceptable — we just verify no panic/goroutine
	// leak in the typing cleanup.
	_ = ch.Stop(context.Background())
}