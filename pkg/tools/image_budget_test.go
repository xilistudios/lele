package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// ---------------------------------------------------------------------------
// T6: origin-side image payload bound (boundImagePayload / read_image wiring).
// Another task appends unrelated tests below this marker.
// ---------------------------------------------------------------------------

// makeNoisyNRGBA builds a deterministic pseudo-random NRGBA image. Random
// noise makes PNG/JPEG poorly compressible, which is what forces the budget
// paths; with alpha set, every pixel gets a translucent random alpha.
func makeNoisyNRGBA(t *testing.T, width, height int, alpha bool) *image.NRGBA {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = byte(rng.Intn(256))
			img.Pix[i+1] = byte(rng.Intn(256))
			img.Pix[i+2] = byte(rng.Intn(256))
			if alpha {
				img.Pix[i+3] = byte(rng.Intn(256))
			} else {
				img.Pix[i+3] = 255
			}
		}
	}
	return img
}

// makeNoisyJPEG builds a deterministic pseudo-random RGB JPEG. Random noise
// makes the JPEG poorly compressible, which is what forces the budget path.
func makeNoisyJPEG(t *testing.T, width, height, quality int) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, makeNoisyNRGBA(t, width, height, false), &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode noisy jpeg: %v", err)
	}
	return buf.Bytes()
}

// makeNoisyPNG builds a deterministic pseudo-random RGB PNG (alpha=false) or
// RGBA PNG (alpha=true). Noise makes it poorly compressible, which is what
// puts a small fixture into the encoded-size bands the tests inject.
func makeNoisyPNG(t *testing.T, width, height int, alpha bool) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, makeNoisyNRGBA(t, width, height, alpha)); err != nil {
		t.Fatalf("encode noisy png: %v", err)
	}
	return buf.Bytes()
}

func TestReadImageTool_Execute_LargeImageIsBounded(t *testing.T) {
	tmpDir := t.TempDir()
	jpegData := makeNoisyJPEG(t, 2400, 1600, 100)
	imagePath := filepath.Join(tmpDir, "big.jpg")
	if err := os.WriteFile(imagePath, jpegData, 0o644); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}

	tool := NewReadImageTool(tmpDir, true)
	result := tool.Execute(context.Background(), map[string]interface{}{"path": imagePath})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if len(result.ContextMessages) != 1 || len(result.ContextMessages[0].ContentParts) != 2 {
		t.Fatalf("unexpected message shape")
	}
	url := result.ContextMessages[0].ContentParts[1].ImageURL.URL
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("unexpected data url prefix: %q", url[:40])
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatalf("decode data url payload: %v", err)
	}
	if len(payload) > maxImageEncodedCeiling {
		t.Fatalf("payload %d bytes exceeds ceiling %d bytes (input was %d bytes)", len(payload), maxImageEncodedCeiling, len(jpegData))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("decode config of payload: %v", err)
	}
	longSide := max(cfg.Width, cfg.Height)
	if longSide > maxImageLongSide {
		t.Fatalf("payload long side %d exceeds %d", longSide, maxImageLongSide)
	}
}

func TestBoundImagePayload_DownscalesAndHitsTarget(t *testing.T) {
	src := makeNoisyJPEG(t, 2400, 1600, 100)

	data, outMIME, w, h, resized, err := boundImagePayload(src, "image/jpeg", "auto", defaultImageLimits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if !resized {
		t.Fatal("expected resized=true")
	}
	if len(data) > maxImageEncodedCeiling {
		t.Fatalf("payload %d bytes exceeds ceiling %d", len(data), maxImageEncodedCeiling)
	}
	if max(w, h) > maxImageLongSide {
		t.Fatalf("long side %d exceeds %d", max(w, h), maxImageLongSide)
	}
	if outMIME != "image/jpeg" {
		t.Fatalf("outMIME = %q, want image/jpeg", outMIME)
	}
}

func TestBoundImagePayload_NoUpscaleAndPassthrough(t *testing.T) {
	// A tiny 32x32 PNG well under the encodedBytes budget and long-side target.
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = byte(i)
		img.Pix[i+1] = byte(i / 2)
		img.Pix[i+2] = 128
		img.Pix[i+3] = 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	src := buf.Bytes()

	data, outMIME, _, _, resized, err := boundImagePayload(src, "image/png", "auto", defaultImageLimits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if resized {
		t.Fatal("expected resized=false for a small image")
	}
	if !bytes.Equal(data, src) {
		t.Fatalf("payload not byte-identical to input (in=%d out=%d)", len(src), len(data))
	}
	if outMIME != "image/png" {
		t.Fatalf("outMIME = %q, want image/png", outMIME)
	}
}

func TestBoundImagePayload_LowDetailSmallerTarget(t *testing.T) {
	src := makeNoisyJPEG(t, 2400, 1600, 100)

	_, _, w, h, resized, err := boundImagePayload(src, "image/jpeg", "low", defaultImageLimits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if !resized {
		t.Fatal("expected resized=true")
	}
	if max(w, h) > maxImageLongSideLow {
		t.Fatalf("low-detail long side %d exceeds %d", max(w, h), maxImageLongSideLow)
	}
}

func TestBoundImagePayload_PngWithAlphaStaysPng(t *testing.T) {
	// Injected limits drive the fixture through the encoder: 64x64 is over
	// the injected 32 px target, so the fast path cannot take it, and the
	// re-encoded 32x32 PNG (~256 bytes) fits under the 512-byte ceiling.
	// Alpha must survive as PNG — never a JPEG fallback. (The old fixture
	// was 64x64 with default limits, i.e. under the fast path, so it never
	// reached the encoder at all — proven vacuous by mutation.)
	limits := imageLimits{longSide: 32, longSideLow: 16, encodedBytes: 8, encodedCeiling: 512, decodePixels: 1_000_000}
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = byte(i)
		img.Pix[i+1] = 200
		img.Pix[i+2] = 40
		img.Pix[i+3] = byte(i % 200) // some pixels translucent
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	src := buf.Bytes()

	data, outMIME, w, h, resized, err := boundImagePayload(src, "image/png", "auto", limits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if !resized {
		t.Fatal("expected resized=true — the encoder must run, not the fast path")
	}
	if outMIME != "image/png" {
		t.Fatalf("outMIME = %q, want image/png (alpha must stay PNG)", outMIME)
	}
	if w != 32 || h != 32 {
		t.Fatalf("dims = %dx%d, want 32x32 (downscaled to the injected target)", w, h)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "png" {
		t.Fatalf("output does not decode as PNG: format=%q err=%v", format, err)
	}
	// Transparency must survive the re-encode (no white flatten).
	out, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	b := out.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := out.At(x, y).RGBA(); a < 0xffff {
				return // translucent pixel found: alpha preserved
			}
		}
	}
	t.Fatal("alpha channel was flattened away")
}

// TestBoundImagePayload_InBandPngPassesThroughByteIdentical pins the fix for
// review H1.1: a PNG between encodedBytes and encodedCeiling whose pixels are
// within the target already satisfies every stated bound, so it must not be
// decoded or re-encoded at all — byte-identical output, resized=false. Before
// the fix the fast path was gated on encodedBytes, so this image was decoded
// (up to 30 MP) and re-encoded for byte-identical output with resized=true,
// which made read_image claim "resized to ..." when nothing happened.
func TestBoundImagePayload_InBandPngPassesThroughByteIdentical(t *testing.T) {
	limits := imageLimits{longSide: 1568, longSideLow: 768, encodedBytes: 64 << 10, encodedCeiling: 512 << 10, decodePixels: 30_000_000}
	src := makeNoisyPNG(t, 300, 300, false) // ~270 KB: inside the injected band
	if len(src) <= limits.encodedBytes || len(src) > limits.encodedCeiling {
		t.Fatalf("fixture precondition: len(src)=%d not in (%d, %d]", len(src), limits.encodedBytes, limits.encodedCeiling)
	}

	data, outMIME, w, h, resized, err := boundImagePayload(src, "image/png", "auto", limits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if resized {
		t.Fatal("expected resized=false: the in-band PNG already satisfies every bound and must pass through untouched")
	}
	if !bytes.Equal(data, src) {
		t.Fatalf("payload not byte-identical to input (in=%d out=%d)", len(src), len(data))
	}
	if outMIME != "image/png" {
		t.Fatalf("outMIME = %q, want image/png", outMIME)
	}
	if w != 300 || h != 300 {
		t.Fatalf("dims = %dx%d, want the untouched 300x300", w, h)
	}
}

// TestBoundImagePayload_OverCeilingPngKeepsTargetDimensionsAsJpeg pins the
// fix for review H1.2 (order): a PNG over the ceiling WITHOUT alpha falls
// back to a full-size JPEG before any canvas shrink, so the pixel count the
// vision model sees is the target, not 3/4 of it per ladder pass. Before the
// fix the canvas ladder ran first and this input came back at 144x144 PNG.
func TestBoundImagePayload_OverCeilingPngKeepsTargetDimensionsAsJpeg(t *testing.T) {
	limits := imageLimits{longSide: 256, longSideLow: 128, encodedBytes: 1024, encodedCeiling: 64 << 10, decodePixels: 1_000_000}
	src := makeNoisyPNG(t, 256, 256, false) // ~193 KB PNG vs ~43 KB full-size JPEG q80
	if len(src) <= limits.encodedCeiling {
		t.Fatalf("fixture precondition: len(src)=%d must exceed the ceiling %d", len(src), limits.encodedCeiling)
	}

	data, outMIME, w, h, resized, err := boundImagePayload(src, "image/png", "auto", limits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if outMIME != "image/jpeg" {
		t.Fatalf("outMIME = %q, want image/jpeg (over-ceiling PNG falls back to JPEG)", outMIME)
	}
	if w != 256 || h != 256 {
		t.Fatalf("dims = %dx%d, want the target dimensions 256x256 preserved, not canvas-shrunk", w, h)
	}
	if !resized {
		t.Fatal("expected resized=true (the payload was re-encoded)")
	}
	if len(data) > limits.encodedCeiling {
		t.Fatalf("payload %d bytes exceeds ceiling %d", len(data), limits.encodedCeiling)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "jpeg" {
		t.Fatalf("output does not decode as JPEG: format=%q err=%v", format, err)
	}
}

// TestBoundImagePayload_OverCeilingPngWithAlphaFlattensToJpegAtTarget pins
// the fix for review H1.2 for alpha sources: the transparent PNG cannot fit,
// so it is flattened onto white and re-encoded as a full-size JPEG — same
// target dimensions, no canvas shrink. Transparency is traded away only when
// the PNG itself did not fit.
func TestBoundImagePayload_OverCeilingPngWithAlphaFlattensToJpegAtTarget(t *testing.T) {
	limits := imageLimits{longSide: 256, longSideLow: 128, encodedBytes: 1024, encodedCeiling: 64 << 10, decodePixels: 1_000_000}
	src := makeNoisyPNG(t, 256, 256, true) // ~257 KB with alpha vs ~39 KB flattened JPEG q80
	if len(src) <= limits.encodedCeiling {
		t.Fatalf("fixture precondition: len(src)=%d must exceed the ceiling %d", len(src), limits.encodedCeiling)
	}

	data, outMIME, w, h, resized, err := boundImagePayload(src, "image/png", "auto", limits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if outMIME != "image/jpeg" {
		t.Fatalf("outMIME = %q, want image/jpeg (over-ceiling alpha PNG is flattened to JPEG)", outMIME)
	}
	if w != 256 || h != 256 {
		t.Fatalf("dims = %dx%d, want the target dimensions 256x256 preserved, not canvas-shrunk", w, h)
	}
	if !resized {
		t.Fatal("expected resized=true (the payload was re-encoded)")
	}
	if len(data) > limits.encodedCeiling {
		t.Fatalf("payload %d bytes exceeds ceiling %d", len(data), limits.encodedCeiling)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "jpeg" {
		t.Fatalf("output does not decode as JPEG: format=%q err=%v", format, err)
	}
}

// TestBoundImagePayload_FullSizeJpegOverCeilingShrinksCanvas pins the last
// resort of review H1.2: when not even a full-size JPEG fits the ceiling
// (tiny injected limits), the 3/4-per-pass canvas ladder still runs and its
// final flatten-to-JPEG step keeps the payload under the ceiling at reduced
// dimensions (72x72 here, below the 128 px target).
func TestBoundImagePayload_FullSizeJpegOverCeilingShrinksCanvas(t *testing.T) {
	limits := imageLimits{longSide: 128, longSideLow: 64, encodedBytes: 512, encodedCeiling: 6_000, decodePixels: 1_000_000}
	src := makeNoisyPNG(t, 128, 128, false) // ~48 KB; even the q50 full-size JPEG (~7.1 KB) exceeds the ceiling

	data, outMIME, w, h, resized, err := boundImagePayload(src, "image/png", "auto", limits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if outMIME != "image/jpeg" {
		t.Fatalf("outMIME = %q, want image/jpeg (last-resort flatten)", outMIME)
	}
	if max(w, h) >= limits.longSide {
		t.Fatalf("dims = %dx%d, want the canvas ladder to shrink below the %d px target", w, h, limits.longSide)
	}
	if !resized {
		t.Fatal("expected resized=true")
	}
	if len(data) > limits.encodedCeiling {
		t.Fatalf("payload %d bytes exceeds ceiling %d", len(data), limits.encodedCeiling)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "jpeg" {
		t.Fatalf("output does not decode as JPEG: format=%q err=%v", format, err)
	}
}

func TestBoundImagePayload_UndecodableWithinCeilingPassesThrough(t *testing.T) {
	limits := imageLimits{longSide: 64, longSideLow: 32, encodedBytes: 8, encodedCeiling: 1024, decodePixels: 1_000_000}
	// Fake webp: stdlib cannot decode it, but payload is under the ceiling.
	src := append([]byte("RIFF"), 0x10, 0x00, 0x00, 0x00)
	src = append(src, []byte("WEBPVP8 ")...)
	src = append(src, make([]byte, 16)...)

	data, outMIME, _, _, resized, err := boundImagePayload(src, "image/webp", "auto", limits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if resized {
		t.Fatal("expected resized=false for undecodable-under-ceiling")
	}
	if !bytes.Equal(data, src) {
		t.Fatal("expected pass-through bytes for undecodable-under-ceiling")
	}
	if outMIME != "image/webp" {
		t.Fatalf("outMIME = %q, want image/webp", outMIME)
	}
}

func TestBoundImagePayload_UndecodableOverCeilingErrors(t *testing.T) {
	limits := imageLimits{longSide: 64, longSideLow: 32, encodedBytes: 8, encodedCeiling: 32, decodePixels: 1_000_000}
	// Fake webp larger than the ceiling -> no decoder and too big to pass through.
	src := append([]byte("RIFF"), 0x40, 0x00, 0x00, 0x00)
	src = append(src, []byte("WEBPVP8 ")...)
	src = append(src, make([]byte, 128)...)

	_, _, _, _, _, err := boundImagePayload(src, "image/webp", "auto", limits)
	if err == nil {
		t.Fatal("expected error for undecodable-over-ceiling")
	}
	if !strings.Contains(err.Error(), "cannot downscale") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBoundImagePayload_PixelBombRejected(t *testing.T) {
	// Hand-craft a PNG whose IHDR declares 20000x20000 (400 MP) but the file is
	// tiny. Patch a real small PNG's IHDR width/height and fix the CRC.
	small := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	src := buf.Bytes()

	const widthOff = 16 // 8 sig + 4 len + 4 "IHDR"
	binary.BigEndian.PutUint32(src[widthOff:], 20000)
	binary.BigEndian.PutUint32(src[widthOff+4:], 20000)
	// Recompute IHDR CRC over bytes [12, 29) and store at [29, 33).
	crc := crc32.ChecksumIEEE(src[12:29])
	binary.BigEndian.PutUint32(src[29:], crc)

	// Must error (not panic / not OOM) before decoding any pixels.
	_, _, _, _, _, err := boundImagePayload(src, "image/png", "auto", defaultImageLimits)
	if err == nil {
		t.Fatal("expected pixel-bomb rejection")
	}
	if !strings.Contains(err.Error(), "too large to process safely") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T4: request image/byte budget (EnforceImageBudget / RequestOverBudget).
// ---------------------------------------------------------------------------

// makeHeavyImageMessages builds count user messages, each carrying a text part
// and one data: image part whose URL is payloadBytes long (filler from the
// base64 alphabet — no real image is allocated). Content is left empty so
// eviction replaces the heavy part with the text placeholder instead of
// deleting it.
func makeHeavyImageMessages(count, payloadBytes int) []providers.Message {
	payload := strings.Repeat("A", payloadBytes)
	msgs := make([]providers.Message, 0, count)
	for i := 0; i < count; i++ {
		msgs = append(msgs, providers.Message{
			Role: "user",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "describe this"},
				{Type: "image_url", ImageURL: &providers.ImageURL{URL: "data:image/jpeg;base64," + payload}},
			},
		})
	}
	return msgs
}

func TestEnforceImageBudget_DropsOldestFirst(t *testing.T) {
	// Just under 2 MiB per part: 4 survivors plus their JSON overhead stay
	// under the 8 MiB budget, so the count cap alone decides and the four
	// newest parts must survive byte-identical. Exactly 2 MiB (2,097,152)
	// per part would make 4 survivors exceed the budget by themselves and
	// trigger the byte-driven pass (covered by
	// TestEnforceImageBudget_ByteDrivenBeyondCount).
	const partBytes = 2_000_000
	in := makeHeavyImageMessages(6, partBytes)

	out, evicted := EnforceImageBudget(in)

	live, _ := providers.LiveMultimodalParts(out)
	if live > MaxLiveImages {
		t.Fatalf("live heavy parts = %d, want <= %d", live, MaxLiveImages)
	}
	if live != 4 {
		t.Fatalf("live heavy parts = %d, want 4 (count cap only)", live)
	}
	// The eviction count must equal the number of replaced parts.
	if placeholders := 6 - live; evicted != placeholders {
		t.Fatalf("evicted = %d, want %d (the number of replaced parts)", evicted, placeholders)
	}
	// The four newest parts are byte-identical to the input ones.
	for i := 2; i < 6; i++ {
		got, want := out[i].ContentParts[1].ImageURL, in[i].ContentParts[1].ImageURL
		if got == nil || want == nil || got.URL != want.URL {
			t.Fatalf("message %d: newest image part must survive byte-identical", i)
		}
	}
	// The two oldest carry the text placeholder and no heavy part.
	for i := 0; i < 2; i++ {
		if out[i].ContentParts[1].ImageURL != nil {
			t.Fatalf("message %d: expected the image part to be replaced", i)
		}
		if !strings.Contains(out[i].ContentParts[1].Text, "omitted") {
			t.Fatalf("message %d: expected the text placeholder, got %q", i, out[i].ContentParts[1].Text)
		}
	}
	if est := providers.EstimateRequestBytes(out); est > config.DefaultMaxRequestBytes {
		t.Fatalf("estimate %d exceeds budget %d", est, config.DefaultMaxRequestBytes)
	}
}

func TestEnforceImageBudget_ByteDrivenBeyondCount(t *testing.T) {
	// 4 parts x 2.5 MiB ~ 10 MiB: the count cap alone (MaxLiveImages=4) would
	// keep them all, but the byte estimate busts the 8 MiB budget, so the
	// byte-driven pass must evict more (oldest first).
	const partBytes = 2_621_440 // 2.5 MiB
	in := makeHeavyImageMessages(4, partBytes)

	out, evicted := EnforceImageBudget(in)

	live, _ := providers.LiveMultimodalParts(out)
	if live >= MaxLiveImages {
		t.Fatalf("live heavy parts = %d, want < %d (byte budget must evict beyond the count cap)", live, MaxLiveImages)
	}
	if live != 3 {
		t.Fatalf("live heavy parts = %d, want 3", live)
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	if est := providers.EstimateRequestBytes(out); est > config.DefaultMaxRequestBytes {
		t.Fatalf("estimate %d exceeds budget %d", est, config.DefaultMaxRequestBytes)
	}
	// The newest part survives; the oldest was replaced.
	if out[3].ContentParts[1].ImageURL == nil {
		t.Fatal("newest image part should survive")
	}
	if out[0].ContentParts[1].ImageURL != nil {
		t.Fatal("oldest image part should be evicted")
	}
}

func TestEnforceImageBudget_NoImagesIsNoop(t *testing.T) {
	in := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	out, evicted := EnforceImageBudget(in)

	if evicted != 0 {
		t.Fatalf("evicted = %d, want 0", evicted)
	}
	if &out[0] != &in[0] {
		t.Fatal("expected the input slice back (identity), got a copy")
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatal("expected output DeepEqual with the input")
	}
}

func TestRequestOverBudget(t *testing.T) {
	over := []providers.Message{
		{Role: "user", Content: strings.Repeat("X", 9<<20)}, // 9 MiB text body
	}
	if !RequestOverBudget(over) {
		t.Fatal("a message above config.DefaultMaxRequestBytes must be reported over budget")
	}
	under := []providers.Message{
		{Role: "user", Content: "small"},
	}
	if RequestOverBudget(under) {
		t.Fatal("a small message must not be reported over budget")
	}
}
