package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tinyMP4Header is a minimal ISO base-media file header: size 0x18 + "ftyp"
// box with the isom brand. http.DetectContentType matches "ftyp" at offset 4
// for isom/iso2/mp41 brands and reports "video/mp4" (verified empirically).
var tinyMP4Header = []byte{
	0x00, 0x00, 0x00, 0x18, // box size
	0x66, 0x74, 0x79, 0x70, // "ftyp"
	0x69, 0x73, 0x6f, 0x6d, // "isom"
	0x69, 0x73, 0x6f, 0x6d, // "isom"
	0x69, 0x73, 0x6f, 0x32, // "iso2"
	0x6d, 0x70, 0x34, 0x31, // "mp41"
	0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00,
}

// movLikeBytes sniffs as application/octet-stream (a "free" box is not in the
// stdlib sniff table), so a .mov file with this content must be accepted via
// the extension fallback (video/quicktime).
var movLikeBytes = []byte{
	0x00, 0x00, 0x00, 0x20, 0x66, 0x72, 0x65, 0x65, // size + "free"
	0xaa, 0xbb, 0xcc, 0xdd, 0x01, 0x02, 0x03, 0x04,
	0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80,
	0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0x0f,
}

// mkvLikeBytes is not an EBML header, so sniffing reports
// application/octet-stream and the .mkv extension fallback must kick in.
var mkvLikeBytes = []byte{
	0x4d, 0x80, 0x81, 0x62, 0x00, 0x01, 0x02, 0x03,
	0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b,
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}

func writeTinyMP4(t *testing.T, dir string) string {
	t.Helper()
	return writeFile(t, dir, "tiny.mp4", tinyMP4Header)
}

func firstVideoPart(t *testing.T, result *ToolResult) *providersContentVideo {
	t.Helper()

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("ContextMessages len = %d, want 1", len(result.ContextMessages))
	}
	msg := result.ContextMessages[0]
	if msg.Role != "user" {
		t.Fatalf("role = %q, want user", msg.Role)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("ContentParts len = %d, want 2", len(msg.ContentParts))
	}
	if msg.ContentParts[0].Type != "text" {
		t.Fatalf("parts[0].Type = %q, want text", msg.ContentParts[0].Type)
	}
	if msg.ContentParts[1].Type != "video_url" {
		t.Fatalf("parts[1].Type = %q, want video_url", msg.ContentParts[1].Type)
	}
	if msg.ContentParts[1].VideoURL == nil {
		t.Fatal("expected video_url part with VideoURL set")
	}

	return &providersContentVideo{url: msg.ContentParts[1].VideoURL.URL, fps: msg.ContentParts[1].VideoURL.FPS}
}

// providersContentVideo is a tiny assertion carrier for the video_url part.
type providersContentVideo struct {
	url string
	fps float64
}

func TestReadVideoTool_Execute_Success(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":   videoPath,
		"prompt": "Describe this video",
		"fps":    2.5,
	})

	if !result.Silent {
		t.Fatalf("expected silent result")
	}
	video := firstVideoPart(t, result)
	if result.ContextMessages[0].ContentParts[0].Text != "Describe this video" {
		t.Fatalf("text part = %q, want Describe this video", result.ContextMessages[0].ContentParts[0].Text)
	}
	if !strings.HasPrefix(video.url, "data:video/mp4;base64,") {
		t.Fatalf("video url prefix = %q, want data:video/mp4;base64,", video.url)
	}

	// The base64 payload must decode back to the original bytes.
	encoded := strings.TrimPrefix(video.url, "data:video/mp4;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(decoded) != len(tinyMP4Header) {
		t.Fatalf("decoded len = %d, want %d", len(decoded), len(tinyMP4Header))
	}
	for i := range decoded {
		if decoded[i] != tinyMP4Header[i] {
			t.Fatalf("decoded byte %d = %#x, want %#x", i, decoded[i], tinyMP4Header[i])
		}
	}

	if video.fps != 2.5 {
		t.Fatalf("fps = %v, want 2.5", video.fps)
	}
	// ForLLM must be one short line with no base64 payload.
	if strings.Contains(result.ForLLM, "base64,") {
		t.Fatalf("ForLLM must not contain base64 payload: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, encoded) {
		t.Fatalf("ForLLM must not contain the encoded payload: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Video loaded into context") {
		t.Fatalf("unexpected ForLLM: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_DefaultPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": videoPath})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	want := "Analyze the video at " + videoPath + "."
	if got := result.ContextMessages[0].ContentParts[0].Text; got != want {
		t.Fatalf("default prompt = %q, want %q", got, want)
	}
}

func TestReadVideoTool_Execute_ExtensionFallback_MOV(t *testing.T) {
	tmpDir := t.TempDir()
	// Content sniffs as application/octet-stream → .mov extension must win.
	movPath := writeFile(t, tmpDir, "clip.mov", movLikeBytes)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": movPath})

	if result.IsError {
		t.Fatalf("expected success for .mov fallback, got error: %s", result.ForLLM)
	}
	video := firstVideoPart(t, result)
	if !strings.HasPrefix(video.url, "data:video/quicktime;base64,") {
		t.Fatalf("video url prefix = %q, want data:video/quicktime;base64,", video.url)
	}
}

func TestReadVideoTool_Execute_ExtensionFallback_MKV(t *testing.T) {
	tmpDir := t.TempDir()
	// Non-EBML content sniffs as application/octet-stream → .mkv wins.
	mkvPath := writeFile(t, tmpDir, "clip.mkv", mkvLikeBytes)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": mkvPath})

	if result.IsError {
		t.Fatalf("expected success for .mkv fallback, got error: %s", result.ForLLM)
	}
	video := firstVideoPart(t, result)
	if !strings.HasPrefix(video.url, "data:video/x-matroska;base64,") {
		t.Fatalf("video url prefix = %q, want data:video/x-matroska;base64,", video.url)
	}
}

func TestReadVideoTool_Execute_UnsupportedType(t *testing.T) {
	tmpDir := t.TempDir()
	path := writeFile(t, tmpDir, "note.txt", []byte("hello"))

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": path})
	if !result.IsError {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(result.ForLLM, "unsupported video type") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_Directory(t *testing.T) {
	tmpDir := t.TempDir()

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": tmpDir})
	if !result.IsError {
		t.Fatal("expected error for directory path")
	}
	if !strings.Contains(result.ForLLM, "path must be a video file, not a directory") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_TooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)
	// Sparse file just over the cap: stat fails the size check before reading.
	if err := os.Truncate(videoPath, 21<<20); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": videoPath})
	if !result.IsError {
		t.Fatal("expected error for oversized video")
	}
	if !strings.Contains(result.ForLLM, "video is too large") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_WorkspaceRestriction(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := writeFile(t, outsideDir, "outside.mp4", tinyMP4Header)

	tool := NewReadVideoTool(workspace, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": outsidePath})
	if !result.IsError {
		t.Fatal("expected error for path outside workspace")
	}
	if !strings.Contains(result.ForLLM, "access denied") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_URLMode(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)

	const rawURL = "https://example.com/clip.mp4"
	result := tool.Execute(context.Background(), map[string]interface{}{"url": rawURL})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Fatal("expected silent result")
	}

	video := firstVideoPart(t, result)
	// URL mode passes the URL through untouched — no data: prefix, no fetching.
	if video.url != rawURL {
		t.Fatalf("video url = %q, want exact %q", video.url, rawURL)
	}
	if strings.HasPrefix(video.url, "data:") {
		t.Fatalf("URL mode must not use a data: prefix: %q", video.url)
	}
	if video.fps != 0 {
		t.Fatalf("fps = %v, want 0 (absent)", video.fps)
	}
	wantPrompt := "Analyze the video at " + rawURL + "."
	if got := result.ContextMessages[0].ContentParts[0].Text; got != wantPrompt {
		t.Fatalf("default prompt = %q, want %q", got, wantPrompt)
	}
	if !strings.Contains(result.ForLLM, "Video URL loaded into context") {
		t.Fatalf("unexpected ForLLM: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "base64,") {
		t.Fatalf("ForLLM must not contain base64 payload: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_URLSchemeCaseInsensitive(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)

	const rawURL = "HTTPS://Example.com/CLIP.MP4"
	result := tool.Execute(context.Background(), map[string]interface{}{"url": rawURL})
	if result.IsError {
		t.Fatalf("expected success for uppercase scheme, got error: %s", result.ForLLM)
	}
	if got := result.ContextMessages[0].ContentParts[1].VideoURL.URL; got != rawURL {
		t.Fatalf("video url = %q, want exact %q", got, rawURL)
	}
}

func TestReadVideoTool_Execute_URLRejectsNonHTTP(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)

	cases := []string{
		"ftp://example.com/clip.mp4",
		"data:video/mp4;base64,x",
		"file:///tmp/x.mp4",
	}
	for _, rawURL := range cases {
		result := tool.Execute(context.Background(), map[string]interface{}{"url": rawURL})
		if !result.IsError {
			t.Fatalf("expected error for %q", rawURL)
		}
		if !strings.Contains(result.ForLLM, "url must be an http(s) URL") {
			t.Fatalf("unexpected error for %q: %s", rawURL, result.ForLLM)
		}
	}
}

func TestReadVideoTool_Execute_URLRequiresHost(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)

	// http(s) prefix but no host — must be rejected after net/url parsing.
	rejected := []string{
		"http://",
		"https:///x.mp4",
	}
	for _, rawURL := range rejected {
		result := tool.Execute(context.Background(), map[string]interface{}{"url": rawURL})
		if !result.IsError {
			t.Fatalf("expected error for %q", rawURL)
		}
		if !strings.Contains(result.ForLLM, "url must be a valid http(s) URL with a host") {
			t.Fatalf("unexpected error for %q: %s", rawURL, result.ForLLM)
		}
	}

	// A well-formed URL with a host is accepted.
	const goodURL = "https://example.com/clip.mp4"
	result := tool.Execute(context.Background(), map[string]interface{}{"url": goodURL})
	if result.IsError {
		t.Fatalf("expected success for %q, got error: %s", goodURL, result.ForLLM)
	}
}

func TestReadVideoTool_Execute_RequiresPathOrURL(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)

	// Neither path nor url.
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !result.IsError {
		t.Fatal("expected error when both path and url are absent")
	}
	if !strings.Contains(result.ForLLM, "path or url is required") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	// Both path and url.
	videoPath := writeTinyMP4(t, tmpDir)
	result = tool.Execute(context.Background(), map[string]interface{}{
		"path": videoPath,
		"url":  "https://example.com/clip.mp4",
	})
	if !result.IsError {
		t.Fatal("expected error when both path and url are provided")
	}
	if !strings.Contains(result.ForLLM, "provide exactly one of path or url, not both") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

func TestReadVideoTool_Execute_FPSValid(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": videoPath,
		"fps":  2.5,
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	video := firstVideoPart(t, result)
	if video.fps != 2.5 {
		t.Fatalf("fps = %v, want 2.5", video.fps)
	}
}

func TestReadVideoTool_Execute_FPSAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": videoPath})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	video := firstVideoPart(t, result)
	if video.fps != 0 {
		t.Fatalf("fps = %v, want 0 when absent", video.fps)
	}
}

func TestReadVideoTool_Execute_FPSOutOfRange(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	cases := []interface{}{0, 0.0, -1, 61, 60.5}
	for _, fps := range cases {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": videoPath,
			"fps":  fps,
		})
		if !result.IsError {
			t.Fatalf("expected error for fps=%v", fps)
		}
		if !strings.Contains(result.ForLLM, "fps must be greater than 0 and at most 60") {
			t.Fatalf("unexpected error for fps=%v: %s", fps, result.ForLLM)
		}
	}
}

func TestReadVideoTool_FPSUpperBoundAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": videoPath,
		"fps":  60,
	})
	if result.IsError {
		t.Fatalf("fps=60 must be accepted, got error: %s", result.ForLLM)
	}
	if video := firstVideoPart(t, result); video.fps != 60 {
		t.Fatalf("fps = %v, want 60", video.fps)
	}
}

func TestReadVideoTool_Schema(t *testing.T) {
	tool := NewReadVideoTool("/tmp", false, VideoCapabilities{Video: true, Vision: true}, nil)

	if tool.Name() != "read_video" {
		t.Fatalf("name = %q, want read_video", tool.Name())
	}
	if d := tool.Description(); d == "" || !strings.Contains(d, "video_url") {
		t.Fatalf("unexpected description: %q", d)
	}

	params := tool.Parameters()
	if params["type"] != "object" {
		t.Fatalf("type = %v, want object", params["type"])
	}
	// path XOR url is validated in Execute — no "required" key at all.
	if _, hasRequired := params["required"]; hasRequired {
		t.Fatal("schema must not declare a required key")
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties missing or wrong type: %T", params["properties"])
	}
	for _, key := range []string{"path", "url", "prompt", "fps", "mode", "frames"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("missing property %q", key)
		}
	}

	// FIX-9: "Provide exactly one of path or url." is stated once (the
	// top-level description), never repeated in the property descriptions.
	const xorSentence = "Provide exactly one of path or url."
	occurrences := strings.Count(tool.Description(), xorSentence)
	for key, prop := range props {
		p, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}
		desc, _ := p["description"].(string)
		if n := strings.Count(desc, xorSentence); n > 0 {
			occurrences += n
			t.Errorf("property %q repeats %q", key, xorSentence)
		}
	}
	if occurrences != 1 {
		t.Fatalf("%q appears %d times across description+properties, want exactly 1", xorSentence, occurrences)
	}
}

// ---------------------------------------------------------------------------
// Frames mode (keyframes + transcript fallback for vision-only models)
// ---------------------------------------------------------------------------

// countFrameParts returns the number of image_url parts and checks the invariants
// every frames-mode result must satisfy: prompt text part first, every image
// part carries a data:image/jpeg;base64 URL, and the trailing text part (when
// present) is the transcript.
func framesModeParts(t *testing.T, result *ToolResult) (imageParts int, textParts []string) {
	t.Helper()

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !result.Silent {
		t.Fatal("expected silent result")
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("ContextMessages len = %d, want 1", len(result.ContextMessages))
	}
	msg := result.ContextMessages[0]
	if msg.Role != "user" {
		t.Fatalf("role = %q, want user", msg.Role)
	}
	if len(msg.ContentParts) == 0 {
		t.Fatal("expected at least one content part")
	}
	if msg.ContentParts[0].Type != "text" {
		t.Fatalf("parts[0].Type = %q, want text (prompt first)", msg.ContentParts[0].Type)
	}
	for i, part := range msg.ContentParts[1:] {
		switch part.Type {
		case "image_url":
			if part.ImageURL == nil {
				t.Fatalf("parts[%d]: image_url part with nil ImageURL", i+1)
			}
			if !strings.HasPrefix(part.ImageURL.URL, "data:image/jpeg;base64,") {
				t.Fatalf("parts[%d] url prefix = %q, want data:image/jpeg;base64,", i+1, part.ImageURL.URL)
			}
			imageParts++
		case "text":
			textParts = append(textParts, part.Text)
		default:
			t.Fatalf("parts[%d].Type = %q, want image_url or trailing text", i+1, part.Type)
		}
	}
	return imageParts, textParts
}

// TestReadVideoTool_FramesMode_AutoVisionOnly pins mode=auto on a
// vision-only model: the frames pipeline runs and delivers keyframes as
// image_url parts (needs ffmpeg on this machine — makeTestVideo skips
// otherwise).
func TestReadVideoTool_FramesMode_AutoVisionOnly(t *testing.T) {
	requireFFmpeg(t)
	tmpDir := t.TempDir()
	videoPath, _ := makeTestVideo(t, tmpDir, 4)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path":   videoPath,
		"prompt": "What happens here?",
		"frames": 3,
	})

	imageParts, textParts := framesModeParts(t, result)
	if imageParts < 1 {
		t.Fatalf("got %d image_url parts, want >= 1", imageParts)
	}
	if got := result.ContextMessages[0].ContentParts[0].Text; got != "What happens here?" {
		t.Fatalf("prompt part = %q, want What happens here?", got)
	}
	// transcribe is nil → no transcript text part after the prompt.
	if len(textParts) != 0 {
		t.Fatalf("expected no transcript text part without a transcriber, got %v", textParts)
	}
	if !strings.Contains(result.ForLLM, "keyframes") {
		t.Fatalf("ForLLM should mention keyframes: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "base64,") {
		t.Fatalf("ForLLM must not contain base64 payload: %s", result.ForLLM)
	}
}

// TestReadVideoTool_FramesMode_Transcribe adds a stub transcriber: the result
// must carry an "Audio transcript:" text part containing the transcript
// (makeTestVideo fixtures carry a sine audio track).
func TestReadVideoTool_FramesMode_Transcribe(t *testing.T) {
	requireFFmpeg(t)
	tmpDir := t.TempDir()
	videoPath, hasAudio := makeTestVideo(t, tmpDir, 4)
	if !hasAudio {
		t.Skip("test fixture has no audio track")
	}

	transcribe := func(_ context.Context, audioPath string) (string, error) {
		if !strings.HasSuffix(audioPath, ".wav") {
			t.Errorf("transcribe got %q, want a .wav path", audioPath)
		}
		return "hello world", nil
	}
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, transcribe)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": videoPath,
		"mode": "frames",
	})

	imageParts, textParts := framesModeParts(t, result)
	if imageParts < 1 {
		t.Fatalf("got %d image_url parts, want >= 1", imageParts)
	}
	if len(textParts) != 1 {
		t.Fatalf("got %d transcript text parts, want 1: %v", len(textParts), textParts)
	}
	if !strings.HasPrefix(textParts[0], "Audio transcript:\n") {
		t.Fatalf("transcript part = %q, want Audio transcript:\\n prefix", textParts[0])
	}
	if !strings.Contains(textParts[0], "hello world") {
		t.Fatalf("transcript part missing stub text: %q", textParts[0])
	}
}

// TestReadVideoTool_FramesMode_RequiresFFmpeg pins the missing-binary error:
// without ffmpeg/ffprobe on PATH the tool must suggest native video_url.
func TestReadVideoTool_FramesMode_RequiresFFmpeg(t *testing.T) {
	if _, ok := ffmpegAvailable(); ok {
		if _, ok := ffprobeAvailable(); ok {
			t.Skip("ffmpeg installed; cannot simulate a missing binary")
		}
	}
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": videoPath})
	if !result.IsError {
		t.Fatal("expected error when ffmpeg/ffprobe are missing")
	}
	if !strings.Contains(result.ForLLM, "frames mode requires ffmpeg and ffprobe on PATH") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

// TestReadVideoTool_FramesMode_CapabilityMatrix pins the mode/capability
// errors: neither capability, native without video, frames without vision.
func TestReadVideoTool_FramesMode_CapabilityMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	t.Run("neither video nor vision", func(t *testing.T) {
		tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: false}, nil)
		result := tool.Execute(context.Background(), map[string]interface{}{"path": videoPath})
		if !result.IsError {
			t.Fatal("expected error for a model without video or vision")
		}
		if !strings.Contains(result.ForLLM, "supports neither native video nor image frames") {
			t.Fatalf("unexpected error: %s", result.ForLLM)
		}
	})

	t.Run("native requires video", func(t *testing.T) {
		tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": videoPath,
			"mode": "native",
		})
		if !result.IsError {
			t.Fatal("expected error for native mode without video support")
		}
		if !strings.Contains(result.ForLLM, "does not support native video input") {
			t.Fatalf("unexpected error: %s", result.ForLLM)
		}
	})

	t.Run("frames requires vision", func(t *testing.T) {
		tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: false}, nil)
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": videoPath,
			"mode": "frames",
		})
		if !result.IsError {
			t.Fatal("expected error for frames mode without vision support")
		}
		if !strings.Contains(result.ForLLM, "does not support image input") {
			t.Fatalf("unexpected error: %s", result.ForLLM)
		}
	})
}

// TestReadVideoTool_FramesMode_URLRejected pins that frames mode accepts
// local paths only — URL input is native-only.
func TestReadVideoTool_FramesMode_URLRejected(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"url":  "https://example.com/clip.mp4",
		"mode": "frames",
	})
	if !result.IsError {
		t.Fatal("expected error for frames mode with a url input")
	}
	if !strings.Contains(result.ForLLM, "frames mode requires a local path; url input is native-only") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

// TestReadVideoTool_ModeValidation pins that an unknown mode is rejected
// before anything else runs (mode validation precedes capability resolution).
func TestReadVideoTool_ModeValidation(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{"mode": "bogus"})
	if !result.IsError {
		t.Fatal("expected error for an unknown mode")
	}
	if !strings.Contains(result.ForLLM, "mode must be one of: auto, native, frames") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

// TestReadVideoTool_FramesBounds pins the 1..16 keyframe bounds, and that a
// stray frames argument on a native call is NOT validated (only frames-mode
// calls care about it).
func TestReadVideoTool_FramesBounds(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	for _, frames := range []interface{}{0, 17, -1, 100} {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path":   videoPath,
			"mode":   "frames",
			"frames": frames,
		})
		if !result.IsError {
			t.Fatalf("expected error for frames=%v", frames)
		}
		if !strings.Contains(result.ForLLM, "frames must be between 1 and 16") {
			t.Fatalf("unexpected error for frames=%v: %s", frames, result.ForLLM)
		}
	}

	// Native call with a stray out-of-bounds frames arg: ignored, not rejected.
	native := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	result := native.Execute(context.Background(), map[string]interface{}{
		"path":   videoPath,
		"frames": 99,
	})
	if result.IsError {
		t.Fatalf("stray frames arg must not reject a native call, got: %s", result.ForLLM)
	}

	// Non-integral and non-numeric forms are rejected outright (no
	// truncation of 16.9, no string/bool coercion — matching the fps
	// policy) with the integer-specific error.
	for _, frames := range []interface{}{16.9, 3.5, "8", true} {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path":   videoPath,
			"mode":   "frames",
			"frames": frames,
		})
		if !result.IsError {
			t.Fatalf("expected error for frames=%v", frames)
		}
		if !strings.Contains(result.ForLLM, "frames must be an integer between 1 and 16") {
			t.Fatalf("unexpected error for frames=%v: %s", frames, result.ForLLM)
		}
	}
}

// TestReadVideoTool_FramesMode_RaisedSizeCap pins the 200 MiB frames cap: a
// 21 MiB file exceeds the native 20 MiB cap but must pass the frames-mode
// size check (it then fails later, at extraction — the point is that
// "video is too large" is NOT the error).
func TestReadVideoTool_FramesMode_RaisedSizeCap(t *testing.T) {
	requireFFmpeg(t)
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)
	if err := os.Truncate(videoPath, 21<<20); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": videoPath,
		"mode": "frames",
	})
	if !result.IsError {
		t.Fatal("expected an error (the sparse file has no decodable duration)")
	}
	if strings.Contains(result.ForLLM, "video is too large") {
		t.Fatalf("frames mode must use the 200 MiB cap, got: %s", result.ForLLM)
	}
}

// TestReadVideoTool_CtxCapsVisionOnlyCtxWins pins per-call capability
// resolution: capabilities stamped via WithVideoCaps win over the
// construction-time snapshot when mode=auto resolves. The tool snapshot here
// is video-capable (the agent's primary model), the stamped ctx caps are
// vision-only (the session/turn model) — the result must deliver frames as
// image_url parts, NOT video_url (requires ffmpeg; makeTestVideo skips
// otherwise).
func TestReadVideoTool_CtxCapsVisionOnlyCtxWins(t *testing.T) {
	requireFFmpeg(t)
	tmpDir := t.TempDir()
	videoPath, _ := makeTestVideo(t, tmpDir, 4)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)
	ctx := WithVideoCaps(context.Background(), VideoCapabilities{Video: false, Vision: true})
	result := tool.Execute(ctx, map[string]interface{}{
		"path":   videoPath,
		"prompt": "What happens here?",
	})

	imageParts, _ := framesModeParts(t, result)
	if imageParts < 1 {
		t.Fatalf("got %d image_url parts, want >= 1 (frames delivery)", imageParts)
	}
	for i, part := range result.ContextMessages[0].ContentParts {
		if part.Type == "video_url" {
			t.Fatalf("parts[%d].Type = video_url, want frames delivery (stamped vision-only caps ignored?)", i)
		}
	}
	if !strings.Contains(result.ForLLM, "keyframes") {
		t.Fatalf("ForLLM should mention keyframes: %s", result.ForLLM)
	}
}

// TestReadVideoTool_CtxCapsVideoCtxWins is the mirror case: the tool
// snapshot is vision-only (frames would be picked without a stamp), the
// stamped ctx caps are video-capable — mode=auto must resolve to native and
// deliver a video_url part.
func TestReadVideoTool_CtxCapsVideoCtxWins(t *testing.T) {
	tmpDir := t.TempDir()
	videoPath := writeTinyMP4(t, tmpDir)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	ctx := WithVideoCaps(context.Background(), VideoCapabilities{Video: true, Vision: true})
	result := tool.Execute(ctx, map[string]interface{}{"path": videoPath})

	video := firstVideoPart(t, result)
	if !strings.HasPrefix(video.url, "data:video/mp4;base64,") {
		t.Fatalf("video url prefix = %q, want data:video/mp4;base64,", video.url)
	}
}

// TestReadVideoTool_FramesMode_RemovesTempDir pins that a successful
// frames-mode Execute leaves no lele-video-frames-* directory behind in
// os.TempDir(): the extracted keyframes are base64'd into the message, so
// nothing has to persist on disk.
func TestReadVideoTool_FramesMode_RemovesTempDir(t *testing.T) {
	requireFFmpeg(t)
	tmpDir := t.TempDir()
	videoPath, _ := makeTestVideo(t, tmpDir, 4)

	before := listFramesTempDirs(t)

	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"path": videoPath,
		"mode": "frames",
	})
	framesModeParts(t, result) // asserts success

	after := listFramesTempDirs(t)
	for name := range after {
		if !before[name] {
			t.Errorf("frames temp dir leaked: %s", name)
		}
	}
}

// listFramesTempDirs returns the lele-video-frames-* entries currently in
// os.TempDir().
func listFramesTempDirs(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read %s: %v", os.TempDir(), err)
	}
	dirs := make(map[string]bool)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "lele-video-frames-") {
			dirs[e.Name()] = true
		}
	}
	return dirs
}

// TestReadVideoTool_FramesMode_TranscriptNoteSuffixes pins the exact
// transcriptNote suffix of the frames-mode ForLLM summary for each of the
// three variants: real transcript attached, transcriber configured but no
// transcript produced, and transcription not configured at all.
func TestReadVideoTool_FramesMode_TranscriptNoteSuffixes(t *testing.T) {
	requireFFmpeg(t)
	tmpDir := t.TempDir()
	videoPath, hasAudio := makeTestVideo(t, tmpDir, 4)

	assertSuffix := func(t *testing.T, result *ToolResult, want string) {
		t.Helper()
		framesModeParts(t, result)
		if !strings.HasSuffix(result.ForLLM, want) {
			t.Fatalf("ForLLM = %q, want suffix %q", result.ForLLM, want)
		}
	}

	t.Run("transcript yes", func(t *testing.T) {
		if !hasAudio {
			t.Skip("test fixture has no audio track")
		}
		stub := func(context.Context, string) (string, error) { return "hello world", nil }
		tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, stub)
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": videoPath,
			"mode": "frames",
		})
		assertSuffix(t, result, "transcript: yes)")
	})

	t.Run("transcription not configured", func(t *testing.T) {
		tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, nil)
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": videoPath,
			"mode": "frames",
		})
		assertSuffix(t, result, "transcription not configured)")
	})

	t.Run("transcript no", func(t *testing.T) {
		if !hasAudio {
			t.Skip("test fixture has no audio track")
		}
		stub := func(context.Context, string) (string, error) {
			return "", errors.New("stub transcriber failure")
		}
		tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: false, Vision: true}, stub)
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": videoPath,
			"mode": "frames",
		})
		assertSuffix(t, result, "transcript: no)")
		// The visible placeholder still reaches the model as a trailing text
		// part — only the summary reports "no transcript attached". Assert on
		// the actual parts so a regression that drops the placeholder (or
		// attaches real transcript content under "transcript: no") is caught.
		imageParts, textParts := framesModeParts(t, result)
		if imageParts < 1 {
			t.Fatalf("expected at least one frame part, got %d", imageParts)
		}
		if len(textParts) == 0 {
			t.Fatal("expected trailing transcript placeholder text part")
		}
		if want := "Audio transcript:\n" + transcriptFailedPrefix; !strings.HasPrefix(textParts[len(textParts)-1], want) {
			t.Fatalf("trailing text part = %q, want prefix %q", textParts[len(textParts)-1], want)
		}
	})
}

// TestIsTranscriptPlaceholder pins the summary/content consistency contract:
// exactly the degradation placeholders report "transcript: no"; real
// transcription text (even if it merely resembles a placeholder) does not.
func TestIsTranscriptPlaceholder(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"no audio", transcriptNoAudio, true},
		{"no speech", transcriptNoSpeech, true},
		{"failure prefix", transcriptFailedPrefix + " stub transcriber failure)", true},
		{"real transcript", "hello world", false},
		{"empty", "", false},
		{"embedded prefix is not a placeholder", "the phrase (audio transcription failed: appears mid-sentence", false},
		{"trailing whitespace differs", transcriptNoSpeech + " ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTranscriptPlaceholder(tc.input); got != tc.want {
				t.Fatalf("isTranscriptPlaceholder(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestReadVideoTool_Execute_FPSValidatedBeforeFileRead pins that a bad fps
// fails before the path/URL dispatch touches the filesystem: a nonexistent
// path would normally fail at stat, so seeing the fps error proves
// validation ran first (no 20 MiB read for a doomed call).
func TestReadVideoTool_Execute_FPSValidatedBeforeFileRead(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewReadVideoTool(tmpDir, true, VideoCapabilities{Video: true, Vision: true}, nil)

	missingPath := filepath.Join(tmpDir, "does-not-exist.mp4")
	for _, fps := range []interface{}{0, 61, -1, 99.5} {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"path": missingPath,
			"fps":  fps,
		})
		if !result.IsError {
			t.Fatalf("expected error for fps=%v", fps)
		}
		if !strings.Contains(result.ForLLM, "fps must be greater than 0 and at most 60") {
			t.Fatalf("unexpected error for fps=%v: %s", fps, result.ForLLM)
		}
	}
}
