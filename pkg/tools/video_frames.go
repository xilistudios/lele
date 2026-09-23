// Frames mode for read_video.
//
// Vision-only models get video understanding through N evenly-spaced JPEG
// keyframes (extractVideoFrames) plus an optional transcript source: the
// audio track pulled as a 16 kHz mono WAV (extractAudioTrack) for a
// downstream speech-to-text step. Both write into a caller-owned temp
// directory; the wiring exists — ReadVideoTool's frames dispatch
// (executeFrames in pkg/tools/video.go) calls this module.
//
// ffmpeg (and ffprobe) are an OPTIONAL runtime dependency resolved at call
// time via exec.LookPath — never a build dependency. Everything here uses
// only the Go standard library and os/exec.

package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// videoFramesTimeout is the overall per-operation timeout (probe + extraction
// share one budget when called through extractVideoFrames).
const videoFramesTimeout = 60 * time.Second

// errSnippetMax bounds the stderr excerpt embedded in errors.
const errSnippetMax = 200

// ffmpegAvailable reports whether the ffmpeg binary is on PATH.
// path is the resolved executable path when ok is true.
func ffmpegAvailable() (path string, ok bool) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", false
	}
	return path, true
}

// ffprobeAvailable reports whether the ffprobe binary is on PATH.
// path is the resolved executable path when ok is true.
func ffprobeAvailable() (path string, ok bool) {
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", false
	}
	return path, true
}

// buildFFProbeDurationArgs builds the argv that prints only the container
// duration (bare float, seconds) of inputPath via ffprobe.
func buildFFProbeDurationArgs(inputPath string) []string {
	return []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	}
}

// buildFFmpegFrameArgs builds the argv that extracts up to frames JPEG frames
// from inputPath into outDir. fpsExpr is the fps filter expression (an fps
// value of N/duration yields ~N evenly-spaced frames across the video),
// -frames:v caps the output count, and the scale filter caps width at 1280
// keeping an even height via -2. The output template frame_%03d.jpg produces
// frame_001.jpg, frame_002.jpg, ...
func buildFFmpegFrameArgs(inputPath string, fpsExpr string, frames int, outDir string) []string {
	return []string{
		"-v", "error",
		"-i", inputPath,
		"-vf", "scale='min(1280,iw)':-2,fps=" + fpsExpr,
		"-frames:v", strconv.Itoa(frames),
		filepath.Join(outDir, "frame_%03d.jpg"),
	}
}

// buildFFmpegAudioArgs builds the argv that demuxes the audio track of
// inputPath (-vn) into a 16 kHz mono PCM WAV at outPath.
func buildFFmpegAudioArgs(inputPath, outPath string) []string {
	return []string{
		"-v", "error",
		"-i", inputPath,
		"-vn", "-ac", "1", "-ar", "16000",
		outPath,
	}
}

// truncateErr trims whitespace and cuts s to errSnippetMax runes, appending
// "..." when truncated. Deliberately local to this file: the snippets here
// are ffmpeg/ffprobe stderr and unrelated to other truncation helpers.
func truncateErr(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= errSnippetMax {
		return s
	}
	return string(runes[:errSnippetMax]) + "..."
}

// probeVideoDuration returns the media duration of inputPath in seconds as
// reported by ffprobe. Errors embed a truncated stderr snippet for
// diagnosis. A parseable but non-positive duration is returned as-is;
// callers decide whether that is usable (extractVideoFrames rejects <= 0).
// The comma decimal separator some locales emit ("3,966000") is tolerated.
func probeVideoDuration(ctx context.Context, inputPath string) (float64, error) {
	ffprobe, ok := ffprobeAvailable()
	if !ok {
		return 0, fmt.Errorf("ffprobe not found on PATH")
	}
	var stdout, stderr strings.Builder
	cmd := exec.CommandContext(ctx, ffprobe, buildFFProbeDurationArgs(inputPath)...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffprobe failed for %q: %w (stderr: %s)",
			inputPath, err, truncateErr(stderr.String()))
	}
	raw := strings.TrimSpace(stdout.String())
	raw = strings.ReplaceAll(raw, ",", ".")
	dur, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse ffprobe duration %q: %w", raw, err)
	}
	return dur, nil
}

// extractVideoFrames writes up to frames evenly-spaced JPEG keyframes of
// inputPath into outDir (which must already exist and be caller-owned) and
// returns the written paths sorted by name (frame_001.jpg, frame_002.jpg, ...).
//
// It probes the duration first and feeds fps=frames/duration to ffmpeg's fps
// filter so ~frames frames are sampled across the whole video; -frames:v
// caps the count and the scale filter keeps width <= 1280 with an even
// height. The whole operation is bounded by videoFramesTimeout.
func extractVideoFrames(ctx context.Context, inputPath string, frames int, outDir string) ([]string, error) {
	ffmpeg, ok := ffmpegAvailable()
	if !ok {
		return nil, fmt.Errorf("ffmpeg not found on PATH")
	}
	if _, ok := ffprobeAvailable(); !ok {
		return nil, fmt.Errorf("ffprobe not found on PATH")
	}

	ctx2, cancel := context.WithTimeout(ctx, videoFramesTimeout)
	defer cancel()

	duration, err := probeVideoDuration(ctx2, inputPath)
	if err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, fmt.Errorf("could not determine video duration for %q", inputPath)
	}

	fpsExpr := strconv.FormatFloat(float64(frames)/duration, 'f', 6, 64)

	cmd := exec.CommandContext(ctx2, ffmpeg, buildFFmpegFrameArgs(inputPath, fpsExpr, frames, outDir)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame extraction failed: %w (stderr: %s)",
			err, truncateErr(stderr.String()))
	}

	paths, err := filepath.Glob(filepath.Join(outDir, "frame_*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("could not list extracted frames: %w", err)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no frames extracted into %q", outDir)
	}
	return paths, nil
}

// extractAudioTrack writes the audio track of inputPath as a 16 kHz mono WAV
// at outDir/audio.wav (outDir must exist and be caller-owned) and returns its
// path. Any ffmpeg failure — including videos without an audio stream — is
// returned as an error wrapping a truncated stderr snippet. No caller
// surfaces that error verbatim: the only caller (ReadVideoTool.transcriptFor
// in video.go) degrades to the visible placeholder "(no audio track
// detected)" for extraction failures, while a failure in the downstream
// speech-to-text step surfaces as "(audio transcription failed: ...)" — the
// read always continues without a transcript. The operation is bounded by
// videoFramesTimeout.
func extractAudioTrack(ctx context.Context, inputPath, outDir string) (wavPath string, err error) {
	ffmpeg, ok := ffmpegAvailable()
	if !ok {
		return "", fmt.Errorf("ffmpeg not found on PATH")
	}

	ctx2, cancel := context.WithTimeout(ctx, videoFramesTimeout)
	defer cancel()

	outPath := filepath.Join(outDir, "audio.wav")
	cmd := exec.CommandContext(ctx2, ffmpeg, buildFFmpegAudioArgs(inputPath, outPath)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg audio extraction failed: %w (stderr: %s)",
			err, truncateErr(stderr.String()))
	}
	return outPath, nil
}
