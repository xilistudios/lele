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
