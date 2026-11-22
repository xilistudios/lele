package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/providers"
)

const maxVideoReadSize = 20 << 20

const maxVideoFramesReadSize = 200 << 20

const (
	defaultVideoFrames = 8
	minVideoFrames     = 1
	maxVideoFrames     = 16
)

const transcriptErrMax = 120

type VideoCapabilities struct {
	Video  bool
	Vision bool
}

const videoCapsCtxKey contextKey = "read_video_caps"

func WithVideoCaps(ctx context.Context, caps VideoCapabilities) context.Context {
	return context.WithValue(ctx, videoCapsCtxKey, caps)
}

func videoCapsFromCtx(ctx context.Context) (caps VideoCapabilities, ok bool) {
	caps, ok = ctx.Value(videoCapsCtxKey).(VideoCapabilities)
	return caps, ok
}

type TranscribeFunc func(ctx context.Context, audioPath string) (string, error)

type ReadVideoTool struct {
	workspace  string
	restrict   bool
	caps       VideoCapabilities
	transcribe TranscribeFunc
}

func NewReadVideoTool(workspace string, restrict bool, caps VideoCapabilities, transcribe TranscribeFunc) *ReadVideoTool {
	return &ReadVideoTool{workspace: workspace, restrict: restrict, caps: caps, transcribe: transcribe}
}

func (t *ReadVideoTool) Name() string {
	return "read_video"
}

func (t *ReadVideoTool) Description() string {
	return "Load a video into the LLM context as native video_url multimodal content (video with audio) for video-capable models, or as keyframes plus an optional audio transcript for vision-capable models. Provide exactly one of path or url."
}

func (t *ReadVideoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to a local video file to read (mp4, m4v, webm, mov, avi, mkv, mpeg, mpg, ogv)",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "Public http(s) URL of a video to reference directly without uploading (YouTube URLs only work on Gemini-backed routes)",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Optional text to send alongside the video for analysis",
			},
			"fps": map[string]interface{}{
				"type":        "number",
				"description": "Optional frame-sampling hint (0 < fps <= 60). When set it is forwarded to the provider inside the video_url object; providers that do not support it (most) ignore it, but a strict endpoint may reject it — use with care. Native mode only.",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"auto", "native", "frames"},
				"description": "How to deliver the video: auto (default) = native video_url when the model supports video, otherwise keyframes+transcript; native = require model video support; frames = keyframes+transcript via ffmpeg (requires vision, local paths only).",
			},
			"frames": map[string]interface{}{
				"type":        "number",
				"description": "frames mode only: keyframes to extract (1-16, default 8)",
			},
		},
	}
}

func (t *ReadVideoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	mode := strings.TrimSpace(asString(args["mode"]))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "native", "frames":
	default:
		return ErrorResult("mode must be one of: auto, native, frames")
	}

	caps := t.caps
	if c, ok := videoCapsFromCtx(ctx); ok {
		caps = c
	}
	resolved := mode
	switch mode {
	case "auto":
		switch {
		case caps.Video:
			resolved = "native"
		case caps.Vision:
			resolved = "frames"
		default:
			return ErrorResult("read_video: the current model supports neither native video nor image frames (vision)")
		}
	case "native":
		if !caps.Video {
			return ErrorResult("read_video native mode: the current model does not support native video input (set the model's video capability or use mode=frames)")
		}
	case "frames":
		if !caps.Vision {
			return ErrorResult("read_video frames mode: the current model does not support image input (vision)")
		}
	}

	frames := defaultVideoFrames
	if resolved == "frames" {
		if raw, ok := args["frames"]; ok && raw != nil {
			n, ok := parseFramesArg(raw)
			if !ok {
				return ErrorResult("frames must be an integer between 1 and 16")
			}
			if n < minVideoFrames || n > maxVideoFrames {
				return ErrorResult("frames must be between 1 and 16")
			}
			frames = n
		}
	}

	path := strings.TrimSpace(asString(args["path"]))
	rawURL := strings.TrimSpace(asString(args["url"]))

	if path == "" && rawURL == "" {
		return ErrorResult("path or url is required")
	}
	if path != "" && rawURL != "" {
		return ErrorResult("provide exactly one of path or url, not both")
	}

	if resolved == "frames" {
		if rawURL != "" {
			return ErrorResult("frames mode requires a local path; url input is native-only")
		}
		return t.executeFrames(ctx, args, path, frames)
	}

	var fps float64
	switch v := args["fps"].(type) {
	case float64:
		if v <= 0 || v > 60 {
			return ErrorResult("fps must be greater than 0 and at most 60")
		}
		fps = v
	case int:
		if v <= 0 || v > 60 {
			return ErrorResult("fps must be greater than 0 and at most 60")
		}
		fps = float64(v)
	}

	var (
		videoURL string
		mimeType string
		data     []byte
		source   string
		forLLM   string
	)

	if rawURL != "" {
		lower := strings.ToLower(rawURL)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			return ErrorResult("url must be an http(s) URL")
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Hostname() == "" {
			return ErrorResult("url must be a valid http(s) URL with a host")
		}
		videoURL = rawURL
		source = rawURL
		forLLM = fmt.Sprintf("Video URL loaded into context: %s", rawURL)
	} else {
		resolvedPath, err := validatePath(path, t.workspace, t.restrict)
		if err != nil {
			return ErrorResult(err.Error())
		}

		fileInfo, err := os.Stat(resolvedPath)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to stat video: %v", err))
		}
		if fileInfo.IsDir() {
			return ErrorResult("path must be a video file, not a directory")
		}
		if fileInfo.Size() > maxVideoReadSize {
			return ErrorResult(fmt.Sprintf("video is too large: %d bytes exceeds %d byte limit", fileInfo.Size(), maxVideoReadSize))
		}

		data, err = os.ReadFile(resolvedPath)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to read video: %v", err))
		}

		mimeType = detectVideoMIME(resolvedPath, data)
		if !isSupportedVideoMIME(mimeType) {
			return ErrorResult(fmt.Sprintf("unsupported video type: %s", mimeType))
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		videoURL = fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
		source = path
		forLLM = fmt.Sprintf("Video loaded into context from %s (%s, %d bytes)", path, mimeType, len(data))
	}

	prompt := strings.TrimSpace(asString(args["prompt"]))
	if prompt == "" {
		prompt = fmt.Sprintf("Analyze the video at %s.", source)
	}

	contextMsg := providers.Message{
		Role: "user",
		ContentParts: []providers.ContentPart{
			{Type: "text", Text: prompt},
			{Type: "video_url", VideoURL: &providers.VideoURL{URL: videoURL, FPS: fps}},
		},
	}

	return &ToolResult{
		ForLLM: forLLM,
		Silent: true,
		ContextMessages: []providers.Message{
			contextMsg,
		},
	}
}

func parseFramesArg(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != math.Trunc(n) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func (t *ReadVideoTool) executeFrames(ctx context.Context, args map[string]interface{}, path string, frames int) *ToolResult {
	resolvedPath, err := validatePath(path, t.workspace, t.restrict)
	if err != nil {
		return ErrorResult(err.Error())
	}

	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to stat video: %v", err))
	}
	if fileInfo.IsDir() {
		return ErrorResult("path must be a video file, not a directory")
	}
	if fileInfo.Size() > maxVideoFramesReadSize {
		return ErrorResult(fmt.Sprintf("video is too large: %d bytes exceeds %d byte limit", fileInfo.Size(), maxVideoFramesReadSize))
	}

	_, hasFFmpeg := ffmpegAvailable()
	_, hasFFprobe := ffprobeAvailable()
	if !hasFFmpeg || !hasFFprobe {
		return ErrorResult("frames mode requires ffmpeg and ffprobe on PATH (or enable the model's video capability for native video_url)")
	}

	outDir, err := os.MkdirTemp("", "lele-video-frames-*")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create temp dir: %v", err))
	}
	defer os.RemoveAll(outDir)

	framePaths, err := extractVideoFrames(ctx, resolvedPath, frames, outDir)
	if err != nil {
		return ErrorResult(fmt.Sprintf("frames extraction failed: %v", err))
	}

	transcript := t.transcriptFor(ctx, resolvedPath, outDir)

	prompt := strings.TrimSpace(asString(args["prompt"]))
	if prompt == "" {
		prompt = fmt.Sprintf("Analyze the video at %s.", path)
	}

	parts := make([]providers.ContentPart, 0, len(framePaths)+2)
	parts = append(parts, providers.ContentPart{Type: "text", Text: prompt})
	for _, framePath := range framePaths {
		raw, err := os.ReadFile(framePath)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to read frame: %v", err))
		}
		parts = append(parts, providers.ContentPart{
			Type: "image_url",
			ImageURL: &providers.ImageURL{
				URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw),
			},
		})
	}
	if transcript != "" {
		parts = append(parts, providers.ContentPart{Type: "text", Text: "Audio transcript:\n" + transcript})
	}

	transcriptNote := "transcription not configured"
	if t.transcribe != nil {
		if transcript != "" && !isTranscriptPlaceholder(transcript) {
			transcriptNote = "transcript: yes"
		} else {
			transcriptNote = "transcript: no"
		}
	}
	forLLM := fmt.Sprintf("Video frames loaded into context from %s (%d keyframes, %s)",
		path, len(framePaths), transcriptNote)

	return &ToolResult{
		ForLLM: forLLM,
		Silent: true,
		ContextMessages: []providers.Message{
			{Role: "user", ContentParts: parts},
		},
	}
}

const (
	transcriptNoAudio      = "(no audio track detected)"
	transcriptNoSpeech     = "(no speech detected)"
	transcriptFailedPrefix = "(audio transcription failed:"
)

func (t *ReadVideoTool) transcriptFor(ctx context.Context, resolvedPath, outDir string) string {
	if t.transcribe == nil {
		return ""
	}
	wavPath, err := extractAudioTrack(ctx, resolvedPath, outDir)
	if err != nil {
		return transcriptNoAudio
	}
	text, err := t.transcribe(ctx, wavPath)
	if err != nil {
		return transcriptFailedPrefix + " " + truncateTranscriptErr(err.Error()) + ")"
	}
	if strings.TrimSpace(text) == "" {
		return transcriptNoSpeech
	}
	return text
}

func isTranscriptPlaceholder(transcript string) bool {
	switch transcript {
	case transcriptNoAudio, transcriptNoSpeech:
		return true
	default:
		return strings.HasPrefix(transcript, transcriptFailedPrefix)
	}
}

func truncateTranscriptErr(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= transcriptErrMax {
		return s
	}
	return string(runes[:transcriptErrMax]) + "..."
}

func detectVideoMIME(path string, data []byte) string {
	sniffed := ""
	if len(data) > 0 {
		sniffLen := len(data)
		if sniffLen > 512 {
			sniffLen = 512
		}
		sniffed = http.DetectContentType(data[:sniffLen])
		if isSupportedVideoMIME(sniffed) {
			return sniffed
		}
	}

	if mimeType := videoMIMEByExtension(path); mimeType != "" {
		return mimeType
	}
	if sniffed != "" {
		return sniffed
	}
	return "application/octet-stream"
}

func videoMIMEByExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	case ".mpeg", ".mpg":
		return "video/mpeg"
	case ".ogv":
		return "video/ogg"
	default:
		return ""
	}
}

func isSupportedVideoMIME(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "video/mp4", "video/webm", "video/quicktime",
		"video/x-msvideo", "video/x-matroska",
		"video/mpeg", "video/ogg":
		return true
	default:
		return false
	}
}
