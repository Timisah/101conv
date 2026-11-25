// Package thumbnails renders bounded, EXIF-oriented image thumbnails using
// pure Go standard-library decoders. It never executes external parsers,
// Office macros, or shell commands.
package thumbnails

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
)

const (
	MaxSourceBytes = 40 << 20   // 40 MiB upper bound on raw image reads
	MaxDimension   = 16384      // Reject decompression bombs above 16384 px
	MaxPixels      = 64_000_000 // Reject images above 64 MP before decoding
	DefaultMaxEdge = 256
)

var (
	ErrUnsupportedFormat = errors.New("unsupported image format for thumbnail")
	ErrImageTooLarge     = errors.New("image dimensions exceed thumbnail safety bounds")
)

// Render decodes a bounded JPEG/PNG/GIF image, applies JPEG EXIF orientation,
// downsamples to fit within maxEdge x maxEdge, and returns encoded thumbnail
// bytes plus MIME type.
func Render(r io.ReadSeeker, maxEdge int) ([]byte, string, error) {
	if maxEdge <= 0 || maxEdge > 512 {
		maxEdge = DefaultMaxEdge
	}
	orientation := readJPEGOrientation(r)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	cfg, format, err := image.DecodeConfig(io.LimitReader(r, MaxSourceBytes))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxDimension || cfg.Height > MaxDimension || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return nil, "", ErrImageTooLarge
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	img, _, err := image.Decode(io.LimitReader(r, MaxSourceBytes))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	if orientation > 1 && orientation <= 8 {
		img = applyOrientation(img, orientation)
	}
	resized := resizeFit(img, maxEdge)

	var buf bytes.Buffer
	if (format == "png" || format == "gif") && hasTransparency(resized) {
		if err := png.Encode(&buf, resized); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 82}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

func resizeFit(src image.Image, maxEdge int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := sw, sh
	if sw > maxEdge || sh > maxEdge {
		if sw >= sh {
			dw = maxEdge
			dh = max(1, (sh*maxEdge+sw/2)/sw)
		} else {
			dh = maxEdge
			dw = max(1, (sw*maxEdge+sh/2)/sh)
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy0 := b.Min.Y + (y*sh)/dh
		sy1 := b.Min.Y + ((y+1)*sh)/dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		if sy1 > b.Max.Y {
			sy1 = b.Max.Y
		}
		for x := 0; x < dw; x++ {
			sx0 := b.Min.X + (x*sw)/dw
			sx1 := b.Min.X + ((x+1)*sw)/dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			if sx1 > b.Max.X {
				sx1 = b.Max.X
			}
			var sr, sg, sb, sa, count uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, bl, a := src.At(sx, sy).RGBA()
					sr += uint64(r)
					sg += uint64(g)
					sb += uint64(bl)
					sa += uint64(a)
					count++
				}
			}
			if count == 0 {
				continue
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((sr / count) >> 8),
				G: uint8((sg / count) >> 8),
				B: uint8((sb / count) >> 8),
				A: uint8((sa / count) >> 8),
			})
		}
	}
	return dst
}

func hasTransparency(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] < 255 {
			return true
		}
	}
	return false
}

func applyOrientation(src image.Image, orient int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	if orient >= 5 && orient <= 8 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var dx, dy int
			switch orient {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			default:
				dx, dy = x, y
			}
			dst.Set(dx, dy, c)
		}
	}
	return dst
}

// readJPEGOrientation inspects at most the first 64 KiB of a JPEG stream for
// an EXIF APP1 Orientation tag (0x0112). Returns 1 when absent or invalid.
func readJPEGOrientation(r io.ReadSeeker) int {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 1
	}
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(r, buf)
	buf = buf[:n]
	if len(buf) < 12 || buf[0] != 0xFF || buf[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 < len(buf) {
		if buf[i] != 0xFF {
			return 1
		}
		marker := buf[i+1]
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		segLen := int(binary.BigEndian.Uint16(buf[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(buf) {
			return 1
		}
		if marker == 0xE1 && segLen >= 16 && string(buf[i+4:i+10]) == "Exif\x00\x00" {
			tiff := buf[i+10 : i+2+segLen]
			return parseTIFFOrientation(tiff)
		}
		i += 2 + segLen
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	for idx := 0; idx < count; idx++ {
		entry := ifdOffset + 2 + idx*12
		if entry+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[entry : entry+2])
		if tag == 0x0112 {
			val := int(order.Uint16(tiff[entry+8 : entry+10]))
			if val >= 1 && val <= 8 {
				return val
			}
			return 1
		}
	}
	return 1
}
