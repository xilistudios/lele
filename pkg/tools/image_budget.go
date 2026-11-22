package tools

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

const (
	maxImageLongSide       = 1568
	maxImageLongSideLow    = 768
	maxImageEncodedBytes   = 1 << 20
	maxImageEncodedCeiling = 3 << 19
	maxImageDecodePixels   = 30_000_000
	jpegQualityHigh        = 80
	jpegQualityMid         = 65
	jpegQualityLow         = 50
)

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

func boundImagePayload(src []byte, mimeType, detail string, limits imageLimits) (data []byte, outMIME string, width, height int, resized bool, err error) {
	target := limits.longSide
	if detail == "low" {
		target = limits.longSideLow
	}

	cfg, _, cfgErr := image.DecodeConfig(bytes.NewReader(src))
	srcW, srcH := 0, 0
	if cfgErr == nil {
		srcW, srcH = cfg.Width, cfg.Height
	}
	if cfgErr != nil {
		if len(src) <= limits.encodedBytes {
			return src, mimeType, 0, 0, false, nil
		}
	} else {
		if int64(cfg.Width)*int64(cfg.Height) > int64(limits.decodePixels) {
			return nil, "", 0, 0, false, fmt.Errorf("image is too large to process safely (%dx%d pixels); resize it before reading", cfg.Width, cfg.Height)
		}
		if len(src) <= limits.encodedCeiling && max(cfg.Width, cfg.Height) <= target {
			return src, mimeType, cfg.Width, cfg.Height, false, nil
		}
	}

	img, _, decErr := image.Decode(bytes.NewReader(src))
	if decErr != nil {
		if len(src) <= limits.encodedCeiling {
			return src, mimeType, srcW, srcH, false, nil
		}
		return nil, "", 0, 0, false, fmt.Errorf("cannot downscale %s (no decoder available); convert it to PNG/JPEG under %d bytes and retry", mimeType, limits.encodedCeiling)
	}

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

	nrgba := toNRGBA(img)
	if dstW != srcW || dstH != srcH {
		nrgba = downscaleNRGBA(nrgba, dstW, dstH)
	}

	hasAlpha := detectAlpha(nrgba)
	usePNG := strings.EqualFold(mimeType, "image/png") || hasAlpha

	var encoded []byte
	outMIME = "image/jpeg"
	if usePNG {
		outMIME = "image/png"
		encoded, err = encodePNGPayload(nrgba)
		if err == nil && len(encoded) > limits.encodedCeiling {
			jpg := nrgba
			if hasAlpha {
				jpg = flattenOnWhite(nrgba)
			}
			for _, quality := range []int{jpegQualityHigh, jpegQualityMid, jpegQualityLow} {
				encoded, err = encodeJPEGPayload(jpg, quality)
				if err != nil || len(encoded) <= limits.encodedCeiling {
					break
				}
			}
			if err == nil && len(encoded) <= limits.encodedCeiling {
				outMIME = "image/jpeg"
			}
		}
		if err == nil && len(encoded) > limits.encodedCeiling {
			for pass := 0; err == nil && len(encoded) > limits.encodedCeiling && pass < 2; pass++ {
				nrgba = downscaleNRGBA(nrgba, max(1, nrgba.Rect.Dx()*3/4), max(1, nrgba.Rect.Dy()*3/4))
				encoded, err = encodePNGPayload(nrgba)
			}
		}
		if err == nil && len(encoded) > limits.encodedCeiling {
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
		return nil, "", 0, 0, false, fmt.Errorf("image payload is still too large after downscaling (%d bytes > %d bytes)", len(encoded), limits.encodedCeiling)
	}

	return encoded, outMIME, nrgba.Rect.Dx(), nrgba.Rect.Dy(), true, nil
}

func detectAlpha(img *image.NRGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] < 255 {
			return true
		}
	}
	return false
}

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

const MaxLiveImages = 4

func RequestOverBudget(messages []providers.Message) bool {
	return providers.EstimateRequestBytes(messages) > config.DefaultMaxRequestBytes
}

func EnforceImageBudget(messages []providers.Message) ([]providers.Message, int) {
	if count, _ := providers.LiveMultimodalParts(messages); count == 0 {
		return messages, 0
	}
	reduced, evicted := providers.ReduceImageParts(messages, MaxLiveImages)
	for keep := MaxLiveImages - 1; keep >= 0 && RequestOverBudget(reduced); keep-- {
		var n int
		reduced, n = providers.ReduceImageParts(reduced, keep)
		evicted += n
	}
	if evicted == 0 {
		return messages, 0
	}
	return reduced, evicted
}
