package channels

import (
	"encoding/json"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestOneBot_NewChannel(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, err := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:3000"}, mb)
	if err != nil {
		t.Fatalf("NewOneBotChannel error: %v", err)
	}
	if ch.Name() != "onebot" {
		t.Errorf("Name() = %q", ch.Name())
	}
	if ch.config.WSUrl != "ws://localhost:3000" {
		t.Errorf("WSUrl not propagated")
	}
}

func TestParseJSONInt64(t *testing.T) {
	// Empty raw → 0, nil.
	if v, err := parseJSONInt64(json.RawMessage{}); err != nil || v != 0 {
		t.Errorf("empty = %d, %v", v, err)
	}
	// Numeric.
	if v, err := parseJSONInt64(json.RawMessage(`123`)); err != nil || v != 123 {
		t.Errorf("numeric = %d, %v", v, err)
	}
	// String number.
	if v, err := parseJSONInt64(json.RawMessage(`"456"`)); err != nil || v != 456 {
		t.Errorf("string = %d, %v", v, err)
	}
	// Unparseable.
	if _, err := parseJSONInt64(json.RawMessage(`abc`)); err == nil {
		t.Error("expected error for unparseable raw")
	}
}

func TestParseJSONString(t *testing.T) {
	if got := parseJSONString(json.RawMessage{}); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := parseJSONString(json.RawMessage(`"hello"`)); got != "hello" {
		t.Errorf("string = %q", got)
	}
	// Non-string raw → returned as-is.
	if got := parseJSONString(json.RawMessage(`123`)); got != "123" {
		t.Errorf("raw = %q", got)
	}
}

func TestIsAPIResponse(t *testing.T) {
	if isAPIResponse(nil) {
		t.Error("empty should be false")
	}
	if !isAPIResponse(json.RawMessage(`"ok"`)) {
		t.Error("\"ok\" should be true")
	}
	if !isAPIResponse(json.RawMessage(`"failed"`)) {
		t.Error("\"failed\" should be true")
	}
	if isAPIResponse(json.RawMessage(`"anything"`)) {
		t.Error("other string should be false")
	}
	// Status object.
	if !isAPIResponse(json.RawMessage(`{"online":true,"good":false}`)) {
		t.Error("online:true should be true")
	}
	if !isAPIResponse(json.RawMessage(`{"online":false,"good":true}`)) {
		t.Error("good:true should be true")
	}
	if isAPIResponse(json.RawMessage(`{"online":false,"good":false}`)) {
		t.Error("both false should be false")
	}
	// Invalid JSON.
	if isAPIResponse(json.RawMessage(`not json`)) {
		t.Error("invalid json should be false")
	}
}

func TestOneBot_parseMessageSegments_String(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())

	// Plain string, no mention.
	res := ch.parseMessageSegments(json.RawMessage(`"hello"`), 0)
	if res.Text != "hello" || res.IsBotMentioned {
		t.Errorf("string res = %+v", res)
	}

	// String containing CQ at for selfID.
	res = ch.parseMessageSegments(json.RawMessage(`"[CQ:at,qq=100] hi"`), 100)
	if res.Text != "hi" || !res.IsBotMentioned {
		t.Errorf("cq at res = %+v", res)
	}

	// String with CQ at but different selfID → no mention.
	res = ch.parseMessageSegments(json.RawMessage(`"[CQ:at,qq=999] hi"`), 100)
	if res.IsBotMentioned {
		t.Errorf("unexpected mention for different qq: %+v", res)
	}
}

func TestOneBot_parseMessageSegments_Invalid(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())
	res := ch.parseMessageSegments(json.RawMessage(`not-json`), 0)
	if res.Text != "" || res.IsBotMentioned {
		t.Errorf("invalid res = %+v", res)
	}
}

func TestOneBot_parseMessageSegments_Segments(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())

	raw := json.RawMessage(`[
		{"type":"text","data":{"text":"hi "}},
		{"type":"at","data":{"qq":"100"}},
		{"type":"text","data":{"text":" there"}},
		{"type":"face","data":{"id":1}},
		{"type":"reply","data":{"id":"12345"}},
		{"type":"forward"}
	]`)
	res := ch.parseMessageSegments(raw, 100)
	if res.Text != "hi  there[face:1][forward message]" {
		t.Errorf("Text = %q", res.Text)
	}
	if !res.IsBotMentioned {
		t.Error("should be mentioned (at 100)")
	}
	if res.ReplyTo != "12345" {
		t.Errorf("ReplyTo = %q", res.ReplyTo)
	}

	// At "all" counts as mention; non-matching user does not.
	raw2 := json.RawMessage(`[
		{"type":"at","data":{"qq":"all"}}
	]`)
	res2 := ch.parseMessageSegments(raw2, 100)
	if !res2.IsBotMentioned {
		t.Error("at all should be mentioned")
	}

	raw3 := json.RawMessage(`[
		{"type":"at","data":{"qq":"999"}}
	]`)
	res3 := ch.parseMessageSegments(raw3, 100)
	if res3.IsBotMentioned {
		t.Error("at other should not be mentioned")
	}
}

func TestOneBot_isDuplicate(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, bus.NewMessageBus())

	if ch.isDuplicate("") {
		t.Error("empty should not be duplicate")
	}
	if ch.isDuplicate("0") {
		t.Error("\"0\" should not be duplicate")
	}

	if ch.isDuplicate("m1") {
		t.Error("first occurrence should not be duplicate")
	}
	if !ch.isDuplicate("m1") {
		t.Error("second occurrence should be duplicate")
	}
	if ch.isDuplicate("m2") || ch.isDuplicate("m3") || ch.isDuplicate("m4") {
		t.Error("new ids should not be duplicates")
	}
}

func TestOneBot_checkGroupTrigger(t *testing.T) {
	ch, _ := NewOneBotChannel(config.OneBotConfig{
		GroupTriggerPrefix: []string{"!", "lele "},
	}, bus.NewMessageBus())

	// Mentioned → triggered, stripped content.
	triggered, content := ch.checkGroupTrigger("  hello  ", true)
	if !triggered || content != "hello" {
		t.Errorf("mentioned case: %v %q", triggered, content)
	}

	// Prefix match.
	triggered, content = ch.checkGroupTrigger("!ping", false)
	if !triggered || content != "ping" {
		t.Errorf("! prefix: %v %q", triggered, content)
	}
	triggered, content = ch.checkGroupTrigger("lele run", false)
	if !triggered || content != "run" {
		t.Errorf("lele prefix: %v %q", triggered, content)
	}

	// Empty prefix skipped, no match.
	ch2, _ := NewOneBotChannel(config.OneBotConfig{GroupTriggerPrefix: []string{""}}, bus.NewMessageBus())
	triggered, content = ch2.checkGroupTrigger("ping", false)
	if triggered || content != "ping" {
		t.Errorf("empty prefix: %v %q", triggered, content)
	}

	// No match → not triggered, original content.
	triggered, content = ch.checkGroupTrigger("ping", false)
	if triggered || content != "ping" {
		t.Errorf("no match: %v %q", triggered, content)
	}
}
