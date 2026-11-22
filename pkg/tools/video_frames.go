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

const videoFramesTimeout = 60 * time.Second

const errSnippetMax = 200

func ffmpegAvailable() (path string, ok bool) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", false
	}
	return path, true
}

func ffprobeAvailable() (path string, ok bool) {
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", false
	}
	return path, true
}

func buildFFProbeDurationArgs(inputPath string) []string {
	return []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	}
}

func buildFFmpegFrameArgs(inputPath string, fpsExpr string, frames int, outDir string) []string {
	return []string{
		"-v", "error",
		"-i", inputPath,
		"-vf", "scale='min(1280,iw)':-2,fps=" + fpsExpr,
		"-frames:v", strconv.Itoa(frames),
		filepath.Join(outDir, "frame_%03d.jpg"),
	}
}

func buildFFmpegAudioArgs(inputPath, outPath string) []string {
	return []string{
		"-v", "error",
		"-i", inputPath,
		"-vn", "-ac", "1", "-ar", "16000",
		outPath,
	}
}

func truncateErr(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= errSnippetMax {
		return s
	}
	return string(runes[:errSnippetMax]) + "..."
}

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
