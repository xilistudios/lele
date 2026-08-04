package protocoltypes

import (
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
		Role:  "user",
		Content: "look at this",
		Media: []string{"/tmp/photo1.jpg", "/tmp/photo2.jpg"},
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
