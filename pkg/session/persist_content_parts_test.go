package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/store"
)

const partsRoundTripImageURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="

func partsRoundTripMessages() []providers.Message {
	return []providers.Message{
		{Role: "user", Content: "text only message"},
		{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "Analyze the image at /tmp/cat.png."},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: partsRoundTripImageURL, Detail: "auto"}},
		}},
		{Role: "user", Content: "look at this", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "look at this"},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: partsRoundTripImageURL}},
		}, ToolCallID: "call_1"},
		{Role: "assistant", Content: "done"},
	}
}

func TestSaveLoad_PreservesContentParts(t *testing.T) {
	st := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(st.Sessions())

	const key = "native:parts-roundtrip"
	seeded := partsRoundTripMessages()
	for _, msg := range seeded {
		sm.AddFullMessage(key, msg)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rows, rErr := st.Sessions().LoadMessagesFromSeq(key, 0)
	if rErr != nil {
		t.Fatalf("LoadMessagesFromSeq: %v", rErr)
	}
	if len(rows) != len(seeded) {
		t.Fatalf("persisted rows = %d, want %d", len(rows), len(seeded))
	}
	for i, row := range rows {
		var stored providers.Message
		if err := json.Unmarshal([]byte(row), &stored); err != nil {
			t.Fatalf("row %d is not a decodable message: %v", i, err)
		}
		if !reflect.DeepEqual(stored, seeded[i]) {
			t.Errorf("row %d = %+v, want %+v", i, stored, seeded[i])
		}
	}

	fresh := NewSessionManager()
	fresh.SetSessionRepo(st.Sessions())
	reloaded := fresh.GetHistory(key)
	if len(reloaded) != len(seeded) {
		t.Fatalf("reloaded messages = %d, want %d", len(reloaded), len(seeded))
	}
	for i := range seeded {
		if !reflect.DeepEqual(reloaded[i], seeded[i]) {
			t.Errorf("message %d after reload = %+v, want %+v", i, reloaded[i], seeded[i])
		}
	}

	wire, wErr := json.Marshal(common.SerializeMessages(reloaded))
	if wErr != nil {
		t.Fatalf("serialize reloaded history: %v", wErr)
	}
	if got := strings.Count(string(wire), "look at this"); got != 1 {
		t.Errorf("wire body repeats the prompt %d times, want 1: %s", got, wire)
	}
	if got := strings.Count(string(wire), partsRoundTripImageURL); got != 2 {
		t.Errorf("wire body carries the image %d times, want 2: %s", got, wire)
	}
}

func TestLoad_LegacyRowWithContentAndParts(t *testing.T) {
	st := newTestStore(t)
	repo := st.Sessions()

	const key = "native:legacy-parts-row"
	now := time.Now()
	if err := repo.UpsertSession(store.SessionMeta{
		Key:       key,
		Name:      "Legacy parts",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	mixed := `{"role":"user","content":"look at this","content_parts":[` +
		`{"type":"text","text":"look at this"},` +
		`{"type":"image_url","image_url":{"url":"` + partsRoundTripImageURL + `","detail":"auto"}}]}`
	partsOnly := `{"role":"user","content":"","content_parts":[` +
		`{"type":"image_url","image_url":{"url":"` + partsRoundTripImageURL + `"}}]}`
	textOnly := `{"role":"assistant","content":"plain text"}`

	rows := []store.MessageRow{
		{Seq: 0, Role: "user", JSON: mixed},
		{Seq: 1, Role: "user", JSON: partsOnly},
		{Seq: 2, Role: "assistant", JSON: textOnly},
	}
	if err := repo.InsertMessages(key, rows); err != nil {
		t.Fatalf("InsertMessages: %v", err)
	}

	sm := NewSessionManager()
	sm.SetSessionRepo(repo)
	history := sm.GetHistory(key)
	if len(history) != len(rows) {
		t.Fatalf("loaded messages = %d, want %d", len(history), len(rows))
	}

	if history[0].Content != "look at this" {
		t.Errorf("legacy mixed content = %q, want %q", history[0].Content, "look at this")
	}
	if len(history[0].ContentParts) != 2 {
		t.Fatalf("legacy mixed parts = %+v, want 2 parts", history[0].ContentParts)
	}
	if history[0].ContentParts[1].ImageURL == nil || history[0].ContentParts[1].ImageURL.URL != partsRoundTripImageURL {
		t.Errorf("legacy mixed image part = %+v, want the stored data URL", history[0].ContentParts[1])
	}

	if history[1].Content != "" {
		t.Errorf("legacy parts-only content = %q, want empty", history[1].Content)
	}
	if len(history[1].ContentParts) != 1 || history[1].ContentParts[0].Type != "image_url" {
		t.Fatalf("legacy parts-only parts = %+v, want the single stored image part", history[1].ContentParts)
	}

	if history[2].Content != "plain text" || len(history[2].ContentParts) != 0 {
		t.Errorf("legacy text-only message = %+v, want content only", history[2])
	}

	wire, wErr := json.Marshal(common.SerializeMessages(history))
	if wErr != nil {
		t.Fatalf("serialize legacy history: %v", wErr)
	}
	if got := strings.Count(string(wire), "look at this"); got != 1 {
		t.Errorf("legacy row repeats its text %d times on the wire, want 1: %s", got, wire)
	}
	if got := strings.Count(string(wire), partsRoundTripImageURL); got != 2 {
		t.Errorf("legacy rows carry the image %d times on the wire, want 2: %s", got, wire)
	}
}
