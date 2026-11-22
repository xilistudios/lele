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

func TestMessageDisplayFieldsRoundTrip(t *testing.T) {
	m := Message{
		Role:           "user",
		Content:        "review the src dir please",
		DisplayContent: "/review src",
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

func TestMessageErrorDetailRoundTrip(t *testing.T) {
	cases := []struct {
		name            string
		msg             Message
		wantErrorDetail string
		wantPersisted   bool
	}{
		{
			name: "failed turn keeps the detail and stays out of context",
			msg: Message{
				Role:               "assistant",
				Content:            "❌ Error processing message: LLM call failed after retries: 429",
				ErrorDetail:        "LLM call failed after retries: 429 Too Many Requests",
				ExcludeFromContext: true,
			},
			wantErrorDetail: "LLM call failed after retries: 429 Too Many Requests",
			wantPersisted:   true,
		},
		{
			name:            "ordinary message omits the key",
			msg:             Message{Role: "assistant", Content: "hi"},
			wantErrorDetail: "",
			wantPersisted:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.msg)
			if err != nil {
				t.Fatal(err)
			}
			hasKey := strings.Contains(string(data), `"error_detail"`)
			if hasKey != tc.wantPersisted {
				t.Fatalf("error_detail present = %v, want %v: %s", hasKey, tc.wantPersisted, data)
			}
			if tc.msg.ExcludeFromContext &&
				!strings.Contains(string(data), `"exclude_from_context":true`) {
				t.Fatalf("the failure message must stay out of the LLM context: %s", data)
			}

			var got Message
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if got.ErrorDetail != tc.wantErrorDetail {
				t.Errorf("round-trip lost ErrorDetail: got %q, want %q", got.ErrorDetail, tc.wantErrorDetail)
			}
			if got.Role != tc.msg.Role || got.Content != tc.msg.Content {
				t.Errorf("round-trip lost role/content: %+v", got)
			}
			if got.ExcludeFromContext != tc.msg.ExcludeFromContext {
				t.Errorf("round-trip lost ExcludeFromContext: got %v, want %v",
					got.ExcludeFromContext, tc.msg.ExcludeFromContext)
			}
		})
	}
}
