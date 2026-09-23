package protocoltypes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextOnlyContent_PlainText(t *testing.T) {
	m := Message{Role: "user", Content: "hello world"}
	if got := m.TextOnlyContent(); got != "hello world" {
		t.Errorf("TextOnlyContent() = %q, want %q", got, "hello world")
	}
}

func TestTextOnlyContent_ContentPartsWithImage(t *testing.T) {
	m := Message{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "Analyze the image at /tmp/cat.png."},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==", Detail: "auto"}},
		},
	}
	got := m.TextOnlyContent()
	want := "Analyze the image at /tmp/cat.png.\n[image]"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
	// Must never contain base64 payload data.
	if strings.Contains(got, "base64") || strings.Contains(got, "iVBORw0KGgo") {
		t.Errorf("TextOnlyContent() leaked image data: %q", got)
	}
}

func TestTextOnlyContent_Media(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "look at this",
		Media:   []string{"/tmp/photo1.jpg", "/tmp/photo2.jpg"},
	}
	got := m.TextOnlyContent()
	want := "look at this\n[media]\n[media]"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
}

func TestTextOnlyContent_EmptyMessage(t *testing.T) {
	m := Message{Role: "assistant"}
	if got := m.TextOnlyContent(); got != "" {
		t.Errorf("TextOnlyContent() = %q, want empty", got)
	}
}

func TestTextOnlyContent_ContentAndPartsCombined(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "main text",
		ContentParts: []ContentPart{
			{Type: "text", Text: "extra text"},
		},
	}
	got := m.TextOnlyContent()
	want := "main text\nextra text"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
}

// --- send_file attachments persistence (WebUI file download) ---

func TestMessageAttachmentsRoundTrip(t *testing.T) {
	m := Message{
		Role:    "assistant",
		Content: "done",
		Attachments: []MessageAttachment{{
			Name: "report.pdf", Path: "/home/u/.lele/tmp/attachments/aa_report.pdf",
			MIMEType: "application/pdf", Kind: "file", Caption: "final",
		}},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"attachments"`) {
		t.Fatalf("marshal must persist attachments for session storage: %s", data)
	}
	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("unmarshal attachments = %d, want 1 (%s)", len(got.Attachments), data)
	}
	a := got.Attachments[0]
	if a.Name != "report.pdf" || a.Path != "/home/u/.lele/tmp/attachments/aa_report.pdf" ||
		a.MIMEType != "application/pdf" || a.Kind != "file" || a.Caption != "final" {
		t.Errorf("round-trip lost fields: %+v", a)
	}
}

func TestMessageOmitsEmptyAttachments(t *testing.T) {
	data, err := json.Marshal(Message{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "attachments") {
		t.Errorf("empty attachments must be omitted from stored JSON: %s", data)
	}
}

// TestMessageDisplayFieldsRoundTrip pins the harness-command display metadata:
// the session store must persist DisplayContent and Command losslessly (they
// re-render the user bubble and the command chip from history) and omit them
// entirely from plain messages so stored JSON stays byte-compatible.
func TestMessageDisplayFieldsRoundTrip(t *testing.T) {
	m := Message{
		Role:           "user",
		Content:        "review the src dir please", // expanded prompt (what the LLM sees)
		DisplayContent: "/review src",               // what the user typed
		Command: &CommandApplied{
			Name: "review", Description: "Review code", Args: "src",
			Agent: "coder", Model: "fast", Source: "workspace",
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"display_content"`, `"harness_command"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("marshal must persist %s for history rendering: %s", key, data)
		}
	}
	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != m.Content || got.DisplayContent != m.DisplayContent {
		t.Errorf("round-trip lost text fields: %+v", got)
	}
	if got.Command == nil || *got.Command != *m.Command {
		t.Errorf("round-trip lost command metadata: %+v", got.Command)
	}

	// Plain messages must not grow the keys (omitempty contract).
	plain, err := json.Marshal(Message{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"display_content", "harness_command"} {
		if strings.Contains(string(plain), key) {
			t.Errorf("plain message must omit %s: %s", key, plain)
		}
	}
}

// --- video_url content parts (read_video tool) ---

// TestMessageVideoPartRoundTrip pins persistence of video content parts:
// the session store round-trips messages through Message.MarshalJSON /
// UnmarshalJSON, so type, url and fps must survive, while fps stays off the
// wire entirely when unset (omitempty).
func TestMessageVideoPartRoundTrip(t *testing.T) {
	m := Message{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "Analyze the video at /tmp/demo.mp4."},
			{Type: "video_url", VideoURL: &VideoURL{URL: "data:video/mp4;base64,AAAA", FPS: 2.0}},
		},
	}
	data, err := json.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"video_url"`) {
		t.Fatalf("marshal must persist the video_url part: %s", data)
	}
	if !strings.Contains(string(data), `"fps"`) {
		t.Fatalf("marshal must persist fps when set: %s", data)
	}

	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ContentParts) != 2 {
		t.Fatalf("content parts = %d, want 2 (%s)", len(got.ContentParts), data)
	}
	video := got.ContentParts[1]
	if video.Type != "video_url" {
		t.Errorf("part type = %q, want video_url", video.Type)
	}
	if video.VideoURL == nil {
		t.Fatal("video_url part lost its VideoURL payload on decode")
	}
	if video.VideoURL.URL != "data:video/mp4;base64,AAAA" {
		t.Errorf("url = %q, want data:video/mp4;base64,AAAA", video.VideoURL.URL)
	}
	if video.VideoURL.FPS != 2.0 {
		t.Errorf("fps = %v, want 2.0", video.VideoURL.FPS)
	}

	// fps must be omitted from the wire when zero.
	plainMsg := Message{
		Role:         "user",
		ContentParts: []ContentPart{{Type: "video_url", VideoURL: &VideoURL{URL: "data:video/mp4;base64,AAAA"}}},
	}
	plain, err := json.Marshal(&plainMsg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"fps"`) {
		t.Errorf("fps must be omitted when zero: %s", plain)
	}
}

// TestTextFromParts_VideoPlaceholder pins the reload path: when a persisted
// message's content is a parts array, UnmarshalJSON rebuilds Content through
// textFromParts, which must keep a "[video]" placeholder (mirroring "[image]")
// so a reloaded message never loses the fact that media was attached.
func TestTextFromParts_VideoPlaceholder(t *testing.T) {
	data := `{"role":"user","content":[` +
		`{"type":"text","text":"Analyze the video"},` +
		`{"type":"video_url","video_url":{"url":"data:video/mp4;base64,AAAA","fps":2.0}},` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo="}}` +
		`]}`
	var m Message
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatal(err)
	}
	want := "Analyze the video\n[video]\n[image]"
	if m.Content != want {
		t.Errorf("Content = %q, want %q", m.Content, want)
	}
	if strings.Contains(m.Content, "base64") || strings.Contains(m.Content, "AAAA") {
		t.Errorf("Content leaked media payload: %q", m.Content)
	}

	// TextContent falls back to textFromParts when Content is empty.
	empty := Message{Role: "user", ContentParts: m.ContentParts}
	if got, want := empty.TextContent(), want; got != want {
		t.Errorf("TextContent() = %q, want %q", got, want)
	}
}

func TestHasVideoContent(t *testing.T) {
	cases := []struct {
		name  string
		parts []ContentPart
		want  bool
	}{
		{
			name:  "non-empty video url",
			parts: []ContentPart{{Type: "video_url", VideoURL: &VideoURL{URL: "https://example.com/v.mp4"}}},
			want:  true,
		},
		{
			name:  "empty video url",
			parts: []ContentPart{{Type: "video_url", VideoURL: &VideoURL{URL: ""}}},
			want:  false,
		},
		{
			name:  "whitespace video url",
			parts: []ContentPart{{Type: "video_url", VideoURL: &VideoURL{URL: "   "}}},
			want:  false,
		},
		{
			name:  "nil VideoURL",
			parts: []ContentPart{{Type: "video_url"}},
			want:  false,
		},
		{
			name:  "image only",
			parts: []ContentPart{{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}}},
			want:  false,
		},
		{
			name:  "text only",
			parts: []ContentPart{{Type: "text", Text: "hello"}},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Role: "user", ContentParts: tc.parts}
			if got := m.HasVideoContent(); got != tc.want {
				t.Errorf("HasVideoContent() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestTextOnlyContent_ContentPartsWithVideo pins the compaction contract:
// video parts render as a "[video]" placeholder and neither the base64
// payload nor the URL ever reaches the summarization model.
func TestTextOnlyContent_ContentPartsWithVideo(t *testing.T) {
	dataURL := "data:video/mp4;base64,AAAA"
	m := Message{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "Analyze the video at /tmp/demo.mp4."},
			{Type: "video_url", VideoURL: &VideoURL{URL: dataURL, FPS: 2.0}},
		},
	}
	got := m.TextOnlyContent()
	want := "Analyze the video at /tmp/demo.mp4.\n[video]"
	if got != want {
		t.Errorf("TextOnlyContent() = %q, want %q", got, want)
	}
	if strings.Contains(got, "base64") {
		t.Errorf("TextOnlyContent() leaked video data: %q", got)
	}
	if strings.Contains(got, dataURL) {
		t.Errorf("TextOnlyContent() leaked the video URL: %q", got)
	}
}
