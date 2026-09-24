package tools

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Pure argv builder tests (no ffmpeg required)
// ---------------------------------------------------------------------------

func TestBuildFFProbeDurationArgs(t *testing.T) {
	got := buildFFProbeDurationArgs("/videos/in.mp4")
	want := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"/videos/in.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildFFProbeDurationArgs() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestBuildFFmpegFrameArgs(t *testing.T) {
	got := buildFFmpegFrameArgs("/videos/in.mp4", "0.750000", 3, "/tmp/frames-out")
	want := []string{
		"-v", "error",
		"-i", "/videos/in.mp4",
		"-vf", "scale='min(1280,iw)':-2,fps=0.750000",
		"-frames:v", "3",
		filepath.Join("/tmp/frames-out", "frame_%03d.jpg"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildFFmpegFrameArgs() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestBuildFFmpegFrameArgs_OutTemplate(t *testing.T) {
	dirs := []string{"/tmp/out", filepath.Join("rel", "dir"), t.TempDir()}
	for _, dir := range dirs {
		got := buildFFmpegFrameArgs("in.mp4", "1.000000", 5, dir)
		last := got[len(got)-1]
		want := filepath.Join(dir, "frame_%03d.jpg")
		if last != want {
			t.Errorf("out template = %q, want %q", last, want)
		}
		if !strings.HasSuffix(last, "frame_%03d.jpg") {
			t.Errorf("out template %q does not end with frame_%%03d.jpg", last)
		}
	}
}

func TestBuildFFmpegAudioArgs(t *testing.T) {
	got := buildFFmpegAudioArgs("/videos/in.mp4", "/tmp/out/audio.wav")
	want := []string{
		"-v", "error",
		"-i", "/videos/in.mp4",
		"-vn", "-ac", "1", "-ar", "16000",
		"/tmp/out/audio.wav",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildFFmpegAudioArgs() =\n%#v\nwant\n%#v", got, want)
	}
}

// fpsExprFor mirrors the fps expression computed by extractVideoFrames:
// frames / duration formatted with 6 decimal places.
func fpsExprFor(frames, duration float64) string {
	return strconv.FormatFloat(frames/duration, 'f', 6, 64)
}

func TestFramesFPSExpressionFormatting(t *testing.T) {
	// Spec case: 2.5/10 -> "0.250000".
	if got := fpsExprFor(2.5, 10); got != "0.250000" {
		t.Errorf("fpsExprFor(2.5, 10) = %q, want %q", got, "0.250000")
	}
	// Integer-frame cases exactly as production computes them.
	cases := []struct {
		frames   int
		duration float64
		want     string
	}{
		{3, 4, "0.750000"},
		{5, 20, "0.250000"},
		{10, 4, "2.500000"},
		{1, 60, "0.016667"},
	}
	for _, c := range cases {
		got := strconv.FormatFloat(float64(c.frames)/c.duration, 'f', 6, 64)
		if got != c.want {
			t.Errorf("frames=%d duration=%v: got %q, want %q", c.frames, c.duration, got, c.want)
		}
		if helper := fpsExprFor(float64(c.frames), c.duration); helper != c.want {
			t.Errorf("fpsExprFor(%d, %v) = %q, want %q", c.frames, c.duration, helper, c.want)
		}
	}
}

func TestBuildFFmpegFrameArgs_UsesFPSExpression(t *testing.T) {
	frames := 3
	fps := fpsExprFor(float64(frames), 4)
	got := buildFFmpegFrameArgs("in.mp4", fps, frames, "/tmp/o")
	if got[5] != "scale='min(1280,iw)':-2,fps=0.750000" {
		t.Errorf("-vf arg = %q, want %q", got[5], "scale='min(1280,iw)':-2,fps=0.750000")
	}
}

func TestFFmpegFFprobeAvailableMatchLookPath(t *testing.T) {
	p, ok := ffmpegAvailable()
	wp, werr := exec.LookPath("ffmpeg")
	if (werr == nil) != ok {
		t.Errorf("ffmpegAvailable ok=%v, exec.LookPath err=%v", ok, werr)
	}
	if ok && p != wp {
		t.Errorf("ffmpegAvailable path=%q, exec.LookPath=%q", p, wp)
	}
	p2, ok2 := ffprobeAvailable()
	wp2, werr2 := exec.LookPath("ffprobe")
	if (werr2 == nil) != ok2 {
		t.Errorf("ffprobeAvailable ok=%v, exec.LookPath err=%v", ok2, werr2)
	}
	if ok2 && p2 != wp2 {
		t.Errorf("ffprobeAvailable path=%q, exec.LookPath=%q", p2, wp2)
	}
}

// ---------------------------------------------------------------------------
// Integration tests (skipped when ffmpeg/ffprobe are not installed)
// ---------------------------------------------------------------------------

// requireFFmpeg skips the test when ffmpeg or ffprobe is missing from PATH.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, ok := ffmpegAvailable(); !ok {
		t.Skip("ffmpeg not installed")
	}
	if _, ok := ffprobeAvailable(); !ok {
		t.Skip("ffprobe not installed")
	}
}

// makeTestVideo generates a short test video in dir and returns its path plus
// whether it carries an audio track. Strategy, in order:
//  1. combined testsrc + sine muxed as libx264/aac,
//  2. same with mpeg4 (libx264 missing),
//  3. video-only fallback (audio mux failed) — hasAudio=false so the audio
//     test expects an error.
//
// It skips the test when fixture generation fails entirely.
func makeTestVideo(t *testing.T, dir string, durationS int) (path string, hasAudio bool) {
	t.Helper()
	ffmpeg, ok := ffmpegAvailable()
	if !ok {
		t.Skip("ffmpeg not installed")
	}
	outPath := filepath.Join(dir, "fixture.mp4")
	videoIn := []string{"-f", "lavfi", "-i",
		fmt.Sprintf("testsrc=duration=%d:size=320x240:rate=10", durationS)}
	audioIn := []string{"-f", "lavfi", "-i",
		fmt.Sprintf("sine=frequency=440:duration=%d", durationS)}

	run := func(args ...string) error {
		output, err := exec.Command(ffmpeg, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, truncateErr(string(output)))
		}
		return nil
	}
	combined := func(vcodec string) []string {
		args := append([]string{"-v", "error"}, videoIn...)
		args = append(args, audioIn...)
		args = append(args, "-pix_fmt", "yuv420p", "-c:v", vcodec, "-c:a", "aac",
			"-shortest", "-y", outPath)
		return args
	}

	if err := run(combined("libx264")...); err == nil {
		return outPath, true
	}
	if err := run(combined("mpeg4")...); err == nil {
		return outPath, true
	}
	videoOnly := append([]string{"-v", "error"}, videoIn...)
	videoOnly = append(videoOnly, "-pix_fmt", "yuv420p", "-c:v", "mpeg4", "-y", outPath)
	if err := run(videoOnly...); err != nil {
		t.Skipf("could not generate test video fixture: %v", err)
	}
	return outPath, false
}

func TestProbeVideoDuration_Integration(t *testing.T) {
	requireFFmpeg(t)
	path, _ := makeTestVideo(t, t.TempDir(), 4)

	dur, err := probeVideoDuration(context.Background(), path)
	if err != nil {
		t.Fatalf("probeVideoDuration: %v", err)
	}
	if dur <= 3.5 || dur >= 4.5 {
		t.Fatalf("duration = %v, want 3.5 < duration < 4.5", dur)
	}
}

func TestExtractVideoFrames_Integration(t *testing.T) {
	requireFFmpeg(t)
	path, _ := makeTestVideo(t, t.TempDir(), 4)
	outDir := t.TempDir()

	paths, err := extractVideoFrames(context.Background(), path, 3, outDir)
	if err != nil {
		t.Fatalf("extractVideoFrames: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("got %d frames, want exactly 3: %v", len(paths), paths)
	}
	wantNames := []string{"frame_001.jpg", "frame_002.jpg", "frame_003.jpg"}
	for i, p := range paths {
		if filepath.Base(p) != wantNames[i] {
			t.Errorf("paths[%d] = %q, want %q", i, p, wantNames[i])
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if fi.Size() <= 1024 {
			t.Errorf("%s is %d bytes, want > 1KB", filepath.Base(p), fi.Size())
		}
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		cfg, format, decErr := image.DecodeConfig(f)
		f.Close()
		if decErr != nil {
			t.Errorf("%s is not a decodable JPEG: %v", filepath.Base(p), decErr)
			continue
		}
		if format != "jpeg" {
			t.Errorf("%s decoded as %q, want jpeg", filepath.Base(p), format)
		}
		if cfg.Width <= 0 || cfg.Height <= 0 {
			t.Errorf("%s has invalid dimensions %dx%d", filepath.Base(p), cfg.Width, cfg.Height)
		}
	}
}

func TestExtractAudioTrack_Integration(t *testing.T) {
	requireFFmpeg(t)
	path, hasAudio := makeTestVideo(t, t.TempDir(), 4)
	outDir := t.TempDir()

	wavPath, err := extractAudioTrack(context.Background(), path, outDir)
	if err != nil {
		// Tolerant behavior: either a WAV exists or an error is returned.
		// No-audio videos are documented to error out; the caller decides
		// how to report it.
		if err.Error() == "" {
			t.Fatal("extractAudioTrack returned an empty error")
		}
		if hasAudio {
			t.Fatalf("extractAudioTrack failed for fixture with audio: %v", err)
		}
		return
	}
	if wavPath == "" {
		t.Fatal("extractAudioTrack returned an empty path without error")
	}
	fi, statErr := os.Stat(wavPath)
	if statErr != nil {
		t.Fatalf("expected %s to exist: %v", wavPath, statErr)
	}
	if hasAudio && fi.Size() <= 10*1024 {
		t.Errorf("audio.wav is %d bytes, want > 10KB", fi.Size())
	}
}

func TestExtractVideoFrames_MissingFile(t *testing.T) {
	requireFFmpeg(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.mp4")

	_, err := extractVideoFrames(context.Background(), missing, 3, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a nonexistent input, got nil")
	}
	t.Logf("missing-file error (as expected): %v", err)
}
