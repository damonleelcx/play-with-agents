package art

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // the model returns PNG

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // and sometimes WebP
)

// MaxBytes is the most a stored cover may weigh.
const MaxBytes = 250_000

// ContentType of every cover this package encodes.
const ContentType = "image/jpeg"

// Normalize decodes a downloaded image, crops it to 16:9 (centre), scales
// it to Width x Height and re-encodes it as JPEG under MaxBytes.
func Normalize(raw []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	b := src.Bounds()
	if b.Dx() < 64 || b.Dy() < 36 {
		return nil, fmt.Errorf("image too small (%dx%d)", b.Dx(), b.Dy())
	}
	// Centre crop to 16:9.
	crop := b
	if b.Dx()*9 > b.Dy()*16 {
		w := b.Dy() * 16 / 9
		crop.Min.X = b.Min.X + (b.Dx()-w)/2
		crop.Max.X = crop.Min.X + w
	} else {
		h := b.Dx() * 9 / 16
		crop.Min.Y = b.Min.Y + (b.Dy()-h)/2
		crop.Max.Y = crop.Min.Y + h
	}
	dst := image.NewRGBA(image.Rect(0, 0, Width, Height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, xdraw.Src, nil)
	return EncodeJPEG(dst)
}

// EncodeJPEG encodes img as JPEG, lowering the quality until it fits
// MaxBytes.
func EncodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	for q := 86; q >= 40; q -= 8 {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if buf.Len() <= MaxBytes {
			return buf.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("cover still %d bytes at the lowest quality", buf.Len())
}
