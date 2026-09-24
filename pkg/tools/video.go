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

// maxVideoReadSize caps local video reads at 20 MiB, mirroring maxImageReadSize.
// Larger files should be referenced through the url parameter instead.
// Applies to native mode only: the whole file is base64-encoded into a
// data: URL, so the encoded payload would grow the message ~1.33x.
const maxVideoReadSize = 20 << 20

// maxVideoFramesReadSize caps the input file for frames mode at 200 MiB.
// Frames mode never base64-encodes the video itself: ffmpeg reads the local
// file and only the extracted JPEG keyframes (a few hundred KB each) are
// encoded into the message, so the 20 MiB native cap would be needlessly
// strict here. Native mode keeps maxVideoReadSize.
const maxVideoFramesReadSize = 200 << 20

// Keyframe count bounds for frames mode (the "frames" argument).
const (
	defaultVideoFrames = 8
	minVideoFrames     = 1
	maxVideoFrames     = 16
)

// transcriptErrMax bounds the transcriber error embedded in the "(audio
// transcription failed: ...)" placeholder shown to the model.
const transcriptErrMax = 120

// VideoCapabilities reports the multimodal capabilities of the model a tool
// call will run against.
type VideoCapabilities struct {
	Video  bool // model accepts native video_url content parts
	Vision bool // model accepts image_url content parts (keyframes fallback)
}

// videoCapsCtxKey carries per-call VideoCapabilities. A private (untyped-const)
// key so no other package can collide with it.
const videoCapsCtxKey contextKey = "read_video_caps"

// WithVideoCaps stamps per-call video capabilities onto ctx. The agent tool
// executor and the subagent runner stamp the caps of the model handling the
// current call (session/turn or subagent model); ReadVideoTool.Execute
// prefers them over the construction-time snapshot, so mode=auto cannot
// emit video_url to a vision-only model (or fall back to frames when native
// delivery is available) after a /model switch.
func WithVideoCaps(ctx context.Context, caps VideoCapabilities) context.Context {
	return context.WithValue(ctx, videoCapsCtxKey, caps)
}

// videoCapsFromCtx returns the per-call capabilities stamped by
// WithVideoCaps. ok is false when no caps were stamped (unit tests, cron
// jobs, callers that never resolved a session model) — the tool then falls
// back to its construction-time snapshot.
func videoCapsFromCtx(ctx context.Context) (caps VideoCapabilities, ok bool) {
	caps, ok = ctx.Value(videoCapsCtxKey).(VideoCapabilities)
	return caps, ok
}

// TranscribeFunc transcribes an audio file (wav) to text; nil when
// transcription is not configured.
type TranscribeFunc func(ctx context.Context, audioPath string) (string, error)

// ReadVideoTool loads a video into the LLM context in one of two modes:
//
//   - native: the video is delivered as a data: URL (local file) or a remote
//     http(s) URL inside a video_url multimodal content part. Used when the
//     model has the video capability flag.
//   - frames: N evenly-spaced JPEG keyframes plus an optional audio
//     transcript are delivered as image_url/text content parts via ffmpeg.
//     Fallback for vision-capable models without native video support
//     (requires local paths only).
//
// It is always registered and filtered from tool definitions when the session
// model supports neither video nor vision.
type ReadVideoTool struct {
	workspace  string
	restrict   bool
	caps       VideoCapabilities
	transcribe TranscribeFunc
}

// NewReadVideoTool creates a read_video tool bound to a workspace.
// When restrict is true, local paths must resolve inside workspace.
// caps snapshot the capabilities of the model the tool runs against (used to
// resolve mode=auto and to gate mode=native/frames); transcribe may be nil
// when audio transcription is not configured (frames mode then omits the
// transcript part).
func NewReadVideoTool(workspace string, restrict bool, caps VideoCapabilities, transcribe TranscribeFunc) *ReadVideoTool {
	return &ReadVideoTool{workspace: workspace, restrict: restrict, caps: caps, transcribe: transcribe}
}

// Name returns the tool name used in tool definitions and executor guards.
func (t *ReadVideoTool) Name() string {
	return "read_video"
}

// Description returns a single-sentence summary for the LLM.
func (t *ReadVideoTool) Description() string {
	return "Load a video into the LLM context as native video_url multimodal content (video with audio) for video-capable models, or as keyframes plus an optional audio transcript for vision-capable models. Provide exactly one of path or url."
}

// Parameters returns the JSON schema for the tool arguments. No key is marked
// required: path XOR url is validated at execution time.
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

// Execute validates the arguments, resolves the delivery mode against the
// model capabilities and loads the video either as native video_url content
// (local bytes or remote URL) or as keyframes+transcript content parts.
func (t *ReadVideoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	// 1. Mode: validated first, before touching the filesystem, so a
	// malformed call fails fast with a clear message.
	mode := strings.TrimSpace(asString(args["mode"]))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "native", "frames":
	default:
		return ErrorResult("mode must be one of: auto, native, frames")
	}

	// 2. Resolve the effective delivery mode against the model capabilities
	// for THIS call: caps stamped on the context by the agent tool executor /
	// subagent runner (session or subagent model) win over the
	// construction-time snapshot, which remains the fallback for callers that
	// resolve no per-call model (unit tests, cron).
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

	// 3. Keyframe count: only meaningful — and only validated — when frames
	// mode may run, so a stray frames argument on a native call is ignored
	// instead of rejected.
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

	// Frames mode reads the local file with ffmpeg; remote URLs are native-only.
	if resolved == "frames" {
		if rawURL != "" {
			return ErrorResult("frames mode requires a local path; url input is native-only")
		}
		return t.executeFrames(ctx, args, path, frames)
	}

	// Optional frame-sampling hint (JSON numbers decode as float64). Absent →
	// stays 0 so the wire format omits it (VideoURL.FPS has omitempty).
	// Native mode only — frames mode returned above and never uses the
	// provider-side hint. Validated BEFORE the path/URL dispatch so a bad fps
	// fails fast, without reading (and base64-encoding) up to 20 MiB first.
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
		videoURL string // data:...;base64,... for local files, raw URL otherwise
		mimeType string // local files only
		data     []byte // local files only
		source   string // value used in the default prompt (path or url)
		forLLM   string
	)

	if rawURL != "" {
		// URL mode: validate scheme and host only. The provider fetches the
		// video server-side (documented trust model — no SSRF/private-IP
		// blocking here), so there is no size check and no MIME sniffing —
		// the data goes on the wire as-is in VideoURL.URL.
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

// parseFramesArg converts the frames argument to an int. Only JSON numbers
// (float64) with an integral value are accepted — 16.9 is rejected, not
// truncated — plus int/int64 from tests and direct callers. Strings, bools
// and other types are rejected (no string coercion), matching the fps
// policy. ok is false when the value is not an integral number.
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

// executeFrames implements the keyframes+transcript delivery: validate the
// local file (raised 200 MiB cap — ffmpeg reads it directly, nothing is
// base64'd), extract N JPEG keyframes with ffmpeg into a temp dir, base64
// them into image_url content parts and append an audio transcript when
// transcription is configured. The temp dir is always removed before
// returning: the frames are base64'd into the message, nothing has to
// persist on disk.
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

	// One short line, never a base64 payload. "transcription not configured"
	// notes that the transcript part was intentionally omitted; otherwise the
	// line reports whether a real transcript is attached — the visible
	// degradation placeholders (no audio track / no speech / transcription
	// failed) count as "transcript: no": a placeholder is not a transcript
	// even though it is attached as a text part so the model knows why.
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

// Visible degradation placeholders transcriptFor returns instead of real
// transcript content. Shared with isTranscriptPlaceholder so the result
// summary and the returned text cannot drift apart.
const (
	transcriptNoAudio      = "(no audio track detected)"
	transcriptNoSpeech     = "(no speech detected)"
	transcriptFailedPrefix = "(audio transcription failed:"
)

// transcriptFor produces the optional audio transcript for frames mode.
// Returns "" when transcription is not configured (the caller then omits the
// transcript part entirely). Failures never abort the read: they degrade to
// a visible placeholder so the model knows why no transcript follows.
func (t *ReadVideoTool) transcriptFor(ctx context.Context, resolvedPath, outDir string) string {
	if t.transcribe == nil {
		return ""
	}
	wavPath, err := extractAudioTrack(ctx, resolvedPath, outDir)
	if err != nil {
		// Videos without an audio stream (or an ffmpeg demux failure) both
		// land here — the model only needs to know there is no transcript.
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

// isTranscriptPlaceholder reports whether transcript is one of the visible
// degradation placeholders instead of real transcribed content. The frames
// result summary uses it to report "transcript: no" for placeholders: they
// are attached as a text part so the model knows why no transcript follows,
// but no transcript actually arrived.
func isTranscriptPlaceholder(transcript string) bool {
	switch transcript {
	case transcriptNoAudio, transcriptNoSpeech:
		return true
	default:
		return strings.HasPrefix(transcript, transcriptFailedPrefix)
	}
}

// truncateTranscriptErr trims whitespace and cuts s to transcriptErrMax
// runes, appending "..." when truncated. Deliberately local: transcriber
// errors are unrelated to the ffmpeg stderr truncation in video_frames.go.
func truncateTranscriptErr(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= transcriptErrMax {
		return s
	}
	return string(runes[:transcriptErrMax]) + "..."
}

// detectVideoMIME resolves the MIME type of a local video. It sniffs the first
// 512 bytes with http.DetectContentType and uses the result only when it is an
// allowlisted video type (video/mp4 for ISO base-media ftyp, video/webm for
// EBML). Otherwise it falls back to an explicit extension map — never to the
// host mime database, which varies across systems and does not know e.g.
// video/x-matroska.
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

// videoMIMEByExtension maps video file extensions to MIME types explicitly.
// Returns "" for unknown extensions so callers can fall back to the sniffed
// value (useful for the "unsupported video type: %s" error message).
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

// isSupportedVideoMIME reports whether the MIME type is in the video allowlist.
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
