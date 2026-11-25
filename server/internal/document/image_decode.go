package document

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
)

const (
	maxImagePixels = 80_000_000
	maxImageDim    = 32768
)

type decodedRaster struct {
	Width           int
	Height          int
	ExifOrientation int
	IsJPEG          bool
	RawBytes        []byte
	Img             image.Image
}

func readJPEGExifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	offset := 2
	for offset+4 <= len(data) {
		if data[offset] != 0xff {
			offset++
			continue
		}
		marker := data[offset+1]
		offset += 2
		if marker == 0xd8 || marker == 0xd9 {
			continue
		}
		if marker == 0xda || offset+2 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 {
			break
		}
		dataStart := offset + 2
		dataEnd := offset + length
		if dataEnd > len(data) {
			break
		}
		if marker == 0xe1 && dataStart+8 <= dataEnd && bytes.Equal(data[dataStart:dataStart+6], []byte("Exif\x00\x00")) {
			tiff := dataStart + 6
			if tiff+8 > dataEnd {
				break
			}
			var bo binary.ByteOrder
			switch string(data[tiff : tiff+2]) {
			case "II":
				bo = binary.LittleEndian
			case "MM":
				bo = binary.BigEndian
			default:
				break
			}
			if bo != nil && bo.Uint16(data[tiff+2:tiff+4]) == 42 {
				ifd := tiff + int(bo.Uint32(data[tiff+4:tiff+8]))
				if ifd >= tiff && ifd+2 <= dataEnd {
					entries := int(bo.Uint16(data[ifd : ifd+2]))
					for i := 0; i < entries; i++ {
						entry := ifd + 2 + i*12
						if entry+12 > dataEnd {
							break
						}
						if bo.Uint16(data[entry:entry+2]) != 0x0112 {
							continue
						}
						typ := bo.Uint16(data[entry+2 : entry+4])
						cnt := bo.Uint32(data[entry+4 : entry+8])
						if typ == 3 && cnt >= 1 {
							val := int(bo.Uint16(data[entry+8 : entry+10]))
							if val >= 1 && val <= 8 {
								return val
							}
						}
					}
				}
			}
		}
		offset = dataEnd
	}
	return 1
}

func exifSwapsAxes(orientation int) bool {
	return orientation >= 5 && orientation <= 8
}

func isJPEGBytes(data []byte) bool {
	return len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
}

func isPNGBytes(data []byte) bool {
	return len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("input file exceeds %d-byte processing limit", maxBytes)
	}
	return data, nil
}

// decodeBoundedImage decodes a raster image with strict pixel and dimension
// limits to prevent decompression bombs.
func decodeBoundedImage(data []byte) (decodedRaster, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return decodedRaster{}, fmt.Errorf("decode image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDim || cfg.Height > maxImageDim || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return decodedRaster{}, fmt.Errorf("image dimensions (%dx%d) exceed safe bounds", cfg.Width, cfg.Height)
	}
	exif := 1
	if format == "jpeg" || isJPEGBytes(data) {
		exif = readJPEGExifOrientation(data)
	}
	if isJPEGBytes(data) && exif == 1 {
		return decodedRaster{
			Width:           cfg.Width,
			Height:          cfg.Height,
			ExifOrientation: 1,
			IsJPEG:          true,
			RawBytes:        data,
		}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return decodedRaster{}, fmt.Errorf("decode image pixels: %w", err)
	}
	if exif != 1 {
		img = applyExifOrientation(img, exif)
	}
	b := img.Bounds()
	return decodedRaster{
		Width:           b.Dx(),
		Height:          b.Dy(),
		ExifOrientation: exif,
		IsJPEG:          isJPEGBytes(data),
		RawBytes:        data,
		Img:             img,
	}, nil
}

func applyExifOrientation(src image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dstW, dstH := w, h
	if exifSwapsAxes(orientation) {
		dstW, dstH = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var dx, dy int
			switch orientation {
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

func rotateImage(src image.Image, degrees int) image.Image {
	q := ((degrees / 90) % 4 + 4) % 4
	if q == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dstW, dstH := w, h
	if q%2 == 1 {
		dstW, dstH = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var dx, dy int
			switch q {
			case 1: // 90 deg CW
				dx, dy = h-1-y, x
			case 2: // 180 deg
				dx, dy = w-1-x, h-1-y
			case 3: // 270 deg CW
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, c)
		}
	}
	return dst
}

func encodePNGBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJPEGBytes(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// flattenToRGB extracts packed 8-bit RGB bytes and an optional 8-bit alpha
// plane (nil when all pixels are fully opaque).
func flattenToRGB(img image.Image, bg string) (rgb []byte, alpha []byte, hasAlpha bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rgb = make([]byte, w*h*3)
	alpha = make([]byte, w*h)
	bgR, bgG, bgB := uint8(255), uint8(255), uint8(255)
	if bg == "black" {
		bgR, bgG, bgB = 0, 0, 0
	}
	idx := 0
	aIdx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			if c.A < 255 {
				hasAlpha = true
			}
			if bg == "transparent" {
				rgb[idx] = c.R
				rgb[idx+1] = c.G
				rgb[idx+2] = c.B
			} else {
				a := uint16(c.A)
				inv := 255 - a
				rgb[idx] = uint8((uint16(c.R)*a + uint16(bgR)*inv + 127) / 255)
				rgb[idx+1] = uint8((uint16(c.G)*a + uint16(bgG)*inv + 127) / 255)
				rgb[idx+2] = uint8((uint16(c.B)*a + uint16(bgB)*inv + 127) / 255)
			}
			alpha[aIdx] = c.A
			idx += 3
			aIdx++
		}
	}
	if !hasAlpha || bg != "transparent" {
		return rgb, nil, false
	}
	return rgb, alpha, true
}
