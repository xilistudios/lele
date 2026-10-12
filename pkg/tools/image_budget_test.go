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
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// T6: origin-side image payload bound (boundImagePayload / read_image wiring).
// Another task appends unrelated tests below this marker.
// ---------------------------------------------------------------------------

// makeNoisyJPEG builds a deterministic pseudo-random RGB JPEG. Random noise
// makes the JPEG poorly compressible, which is what forces the budget path.
func makeNoisyJPEG(t *testing.T, width, height, quality int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = byte(rng.Intn(256))
		img.Pix[i+1] = byte(rng.Intn(256))
		img.Pix[i+2] = byte(rng.Intn(256))
		img.Pix[i+3] = 255
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode noisy jpeg: %v", err)
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

	data, outMIME, _, _, _, err := boundImagePayload(src, "image/png", "auto", defaultImageLimits)
	if err != nil {
		t.Fatalf("boundImagePayload: %v", err)
	}
	if outMIME != "image/png" {
		t.Fatalf("outMIME = %q, want image/png (alpha must stay PNG)", outMIME)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "png" {
		t.Fatalf("output does not decode as PNG: format=%q err=%v", format, err)
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
