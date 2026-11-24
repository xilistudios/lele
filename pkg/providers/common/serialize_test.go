package common

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func jsonRoundTripMessage(t *testing.T, msg Message) Message {
	t.Helper()
	encoded, err := json.Marshal(&msg)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	var reloaded Message
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	return reloaded
}

func wireJSON(t *testing.T, messages []Message) string {
	t.Helper()
	data, err := json.Marshal(SerializeMessages(messages))
	if err != nil {
		t.Fatalf("marshal serialized messages: %v", err)
	}
	return string(data)
}

func TestSerializeMessagesReloadNoDuplicateText(t *testing.T) {
	live := Message{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "Analyze shot.png"},
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,abcd"}},
		},
	}

	reloaded := jsonRoundTripMessage(t, live)
	if reloaded.Content == "" {
		t.Fatal("expected reloaded message to carry synthesized Content")
	}

	liveWire := wireJSON(t, []Message{live})
	reloadedWire := wireJSON(t, []Message{reloaded})
	if liveWire != reloadedWire {
		t.Fatalf("serialized wire body differs after reload:\nlive:     %s\nreloaded: %s", liveWire, reloadedWire)
	}
	if got := strings.Count(reloadedWire, "Analyze shot.png"); got != 1 {
		t.Fatalf("prompt text appears %d times, want 1: %s", got, reloadedWire)
	}
	if strings.Contains(reloadedWire, "[image]") {
		t.Fatalf("serialized body contains the [image] placeholder: %s", reloadedWire)
	}
}

func TestSerializeMessagesMediaStillAppendsContent(t *testing.T) {
	wire := wireJSON(t, []Message{{
		Role:    "user",
		Content: "describe this",
		Media:   []string{"data:image/png;base64,abc123"},
	}})
	if got := strings.Count(wire, "describe this"); got != 1 {
		t.Fatalf("content text appears %d times, want 1: %s", got, wire)
	}
	if !strings.Contains(wire, `"type":"image_url"`) {
		t.Fatalf("media image part missing: %s", wire)
	}
}

func TestReadLimitedBody(t *testing.T) {
	t.Run("under limit", func(t *testing.T) {
		payload := bytes.Repeat([]byte("a"), 1<<20)
		body, err := ReadLimitedBody(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("ReadLimitedBody() error = %v", err)
		}
		if len(body) != len(payload) {
			t.Fatalf("len(body) = %d, want %d", len(body), len(payload))
		}
	})

	t.Run("over limit", func(t *testing.T) {
		body, err := ReadLimitedBody(&repeatingReader{remaining: MaxResponseBodyBytes + 1, fill: 'a'})
		if err == nil {
			t.Fatal("expected an error for a body over the limit, got nil")
		}
		if body != nil {
			t.Fatalf("body = %d bytes, want nil on error", len(body))
		}
		if !strings.Contains(err.Error(), "32 MB") {
			t.Fatalf("error = %q, want it to name the 32 MB limit", err.Error())
		}
	})

	t.Run("at limit", func(t *testing.T) {
		body, err := ReadLimitedBody(&repeatingReader{remaining: MaxResponseBodyBytes, fill: 'a'})
		if err != nil {
			t.Fatalf("ReadLimitedBody() error = %v", err)
		}
		if len(body) != MaxResponseBodyBytes {
			t.Fatalf("len(body) = %d, want %d", len(body), MaxResponseBodyBytes)
		}
	})
}

type repeatingReader struct {
	remaining int
	fill      byte
}

func (r *repeatingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = r.fill
	}
	r.remaining -= n
	return n, nil
}

var errEOF = errReaderEOF{}

type errReaderEOF struct{}

func (errReaderEOF) Error() string { return "EOF" }
