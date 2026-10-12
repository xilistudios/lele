package tools

// This file holds the *origin-side* payload bound for image reads: images are
// downscaled and re-encoded before being embedded as base64 data URLs, so a
// single read_image call can never blow the request byte budget by itself.
// Small images take a fast path and are passed through unchanged.
//
// A later change appends request-budget helpers (eviction/budget enforcement)
// to this file; those are owned by a different task.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // register GIF decoder (first frame only)
	"image/jpeg"
	"image/png"
	"strings"
)

const (
	maxImageLongSide       = 1568       // px, long edge for detail auto/high
	maxImageLongSideLow    = 768        // px, long edge for detail "low"
	maxImageEncodedBytes   = 1 << 20    // 1 MiB binary target per image
	maxImageEncodedCeiling = 3 << 19    // 1.5 MiB encoded hard ceiling (data-URL payload)
	maxImageDecodePixels   = 30_000_000 // 30 MP guard before decoding
	jpegQualityHigh        = 80
	jpegQualityMid         = 65
	jpegQualityLow         = 50
)

// imageLimits bundles the payload bounds so tests can inject tiny values.
type imageLimits struct {
	longSide       int
	longSideLow    int
	encodedBytes   int
	encodedCeiling int
	decodePixels   int
}

var defaultImageLimits = imageLimits{
	longSide:       maxImageLongSide,
	longSideLow:    maxImageLongSideLow,
	encodedBytes:   maxImageEncodedBytes,
	encodedCeiling: maxImageEncodedCeiling,
	decodePixels:   maxImageDecodePixels,
}

// boundImagePayload is the origin-side payload bound for read_image. It
// downscales/re-encodes src so the data-URL payload stays within
// limits.encodedCeiling and the long edge within the detail-dependent target.
//
// It returns the bounded payload, the MIME of that payload, the final pixel
// dimensions (width, height; only meaningful when resized), and whether the
// image was resized or re-encoded (resized==false means src was passed through
// byte-identical). The extra width/height results keep the call site free of a
// second DecodeConfig pass; the function stays unexported.
func boundImagePayload(src []byte, mimeType, detail string, limits imageLimits) (data []byte, outMIME string, width, height int, resized bool, err error) {
	target := limits.longSide
	if detail == "low" {
		target = limits.longSideLow
	}

	// Step 1+2: fast path and pixel-bomb guard. DecodeConfig only reads the
	// header, so the guard runs before any pixel allocation.
	cfg, _, cfgErr := image.DecodeConfig(bytes.NewReader(src))
	srcW, srcH := 0, 0
	if cfgErr == nil {
		srcW, srcH = cfg.Width, cfg.Height
	}
	if cfgErr != nil {
		// Undecodable format (e.g. webp): the size condition alone is
		// sufficient for the fast path.
		if len(src) <= limits.encodedBytes {
			return src, mimeType, 0, 0, false, nil
		}
	} else {
		if int64(cfg.Width)*int64(cfg.Height) > int64(limits.decodePixels) {
			return nil, "", 0, 0, false, fmt.Errorf("image is too large to process safely (%dx%d pixels); resize it before reading", cfg.Width, cfg.Height)
		}
		if len(src) <= limits.encodedBytes && max(cfg.Width, cfg.Height) <= target {
			// Fast path: small image already within target — zero behaviour
			// change, bytes passed through untouched.
			return src, mimeType, cfg.Width, cfg.Height, false, nil
		}
	}

	// Step 3: full decode (GIF yields its first frame).
	img, _, decErr := image.Decode(bytes.NewReader(src))
	if decErr != nil {
		// No stdlib decoder (webp) or corrupt data: keep today's behaviour
		// for small payloads, error out for large ones.
		if len(src) <= limits.encodedCeiling {
			return src, mimeType, srcW, srcH, false, nil
		}
		return nil, "", 0, 0, false, fmt.Errorf("cannot downscale %s (no decoder available); convert it to PNG/JPEG under %d bytes and retry", mimeType, limits.encodedCeiling)
	}

	// Step 4: compute the target long side. Never upscale — if the decoded
	// long side is already within target the dimensions are kept and only the
	// bytes are re-encoded.
	bounds := img.Bounds()
	srcW, srcH = bounds.Dx(), bounds.Dy()
	dstW, dstH := srcW, srcH
	if long := max(srcW, srcH); long > target {
		if srcW >= srcH {
			dstW = target
			dstH = max(1, (srcH*target+srcW/2)/srcW)
		} else {
			dstH = target
			dstW = max(1, (srcW*target+srcH/2)/srcH)
		}
	}

	// Step 5: convert to NRGBA and downscale with a box (area-average) filter.
	nrgba := toNRGBA(img)
	if dstW != srcW || dstH != srcH {
		nrgba = downscaleNRGBA(nrgba, dstW, dstH)
	}

	// Step 6: PNG when the source was PNG or any pixel is translucent;
	// otherwise JPEG.
	usePNG := strings.EqualFold(mimeType, "image/png") || detectAlpha(nrgba)

	// Step 7: byte ladder until the payload fits the ceiling.
	var encoded []byte
	outMIME = "image/jpeg"
	if usePNG {
		outMIME = "image/png"
		encoded, err = encodePNGPayload(nrgba)
		// PNG has no quality knob: shrink the canvas by 3/4 per pass (at most
		// two extra passes) and re-encode.
		for pass := 0; err == nil && len(encoded) > limits.encodedCeiling && pass < 2; pass++ {
			nrgba = downscaleNRGBA(nrgba, max(1, nrgba.Rect.Dx()*3/4), max(1, nrgba.Rect.Dy()*3/4))
			encoded, err = encodePNGPayload(nrgba)
		}
		if err == nil && len(encoded) > limits.encodedCeiling {
			// Explicit size-over-transparency trade-off: alpha can cost
			// megabytes, so flatten onto white and fall back to JPEG.
			encoded, err = encodeJPEGPayload(flattenOnWhite(nrgba), jpegQualityMid)
			outMIME = "image/jpeg"
		}
	} else {
		for _, quality := range []int{jpegQualityHigh, jpegQualityMid, jpegQualityLow} {
			encoded, err = encodeJPEGPayload(nrgba, quality)
			if err != nil || len(encoded) <= limits.encodedCeiling {
				break
			}
		}
	}
	if err != nil {
		return nil, "", 0, 0, false, fmt.Errorf("re-encode image: %w", err)
	}
	if len(encoded) > limits.encodedCeiling {
		// Unreachable in practice (the ladder covers it); never silently
		// return an oversized payload.
		return nil, "", 0, 0, false, fmt.Errorf("image payload is still too large after downscaling (%d bytes > %d bytes)", len(encoded), limits.encodedCeiling)
	}

	// Step 8: bytes were re-encoded, so resized is true even if dimensions
	// were kept.
	return encoded, outMIME, nrgba.Rect.Dx(), nrgba.Rect.Dy(), true, nil
}

// detectAlpha reports whether any pixel of img is translucent (A < 255),
// scanning with an early exit.
func detectAlpha(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] < 255 {
			return true
		}
	}
	return false
}

// toNRGBA converts any decoded image to a tightly packed *image.NRGBA via a
// plain per-pixel copy loop (no image/draw dependency).
func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			i := dst.PixOffset(x, y)
			dst.Pix[i] = c.R
			dst.Pix[i+1] = c.G
			dst.Pix[i+2] = c.B
			dst.Pix[i+3] = c.A
		}
	}
	return dst
}

// downscaleNRGBA shrinks src to dstW x dstH with a single-pass box
// (area-average) filter over destination pixels. It never upscales (callers
// must not ask for larger dimensions) and preserves aspect ratio.
func downscaleNRGBA(src *image.NRGBA, dstW, dstH int) *image.NRGBA {
	srcW, srcH := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for dy := 0; dy < dstH; dy++ {
		sy0 := dy * srcH / dstH
		sy1 := (dy + 1) * srcH / dstH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		if sy1 > srcH {
			sy1 = srcH
		}
		for dx := 0; dx < dstW; dx++ {
			sx0 := dx * srcW / dstW
			sx1 := (dx + 1) * srcW / dstW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			if sx1 > srcW {
				sx1 = srcW
			}
			var r, g, b, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				off := src.PixOffset(0, sy)
				for sx := sx0; sx < sx1; sx++ {
					i := off + sx*4
					r += uint64(src.Pix[i])
					g += uint64(src.Pix[i+1])
					b += uint64(src.Pix[i+2])
					a += uint64(src.Pix[i+3])
					n++
				}
			}
			di := dst.PixOffset(dx, dy)
			dst.Pix[di] = uint8(r / n)
			dst.Pix[di+1] = uint8(g / n)
			dst.Pix[di+2] = uint8(b / n)
			dst.Pix[di+3] = uint8(a / n)
		}
	}
	return dst
}

// flattenOnWhite composites src over a white background, discarding alpha.
func flattenOnWhite(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Rect)
	copy(dst.Pix, src.Pix)
	for i := 0; i < len(dst.Pix); i += 4 {
		a := uint32(dst.Pix[i+3])
		if a == 255 {
			continue
		}
		dst.Pix[i] = uint8((uint32(dst.Pix[i])*a + 255*(255-a) + 127) / 255)
		dst.Pix[i+1] = uint8((uint32(dst.Pix[i+1])*a + 255*(255-a) + 127) / 255)
		dst.Pix[i+2] = uint8((uint32(dst.Pix[i+2])*a + 255*(255-a) + 127) / 255)
		dst.Pix[i+3] = 255
	}
	return dst
}

func encodePNGPayload(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJPEGPayload(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
