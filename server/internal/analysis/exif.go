package analysis

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

const maxEXIFValueBytes = 256 << 10

// parseJPEGEXIF reads JPEG APP1 EXIF/TIFF metadata without depending on a
// platform utility. It keeps unknown tags as namespaced values, follows the
// EXIF and GPS IFD pointers, and adds decimal GPS coordinates for display.
func parseJPEGEXIF(data []byte) map[string]any {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			offset++
			continue
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker == 0xd8 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		segmentLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > len(data) {
			break
		}
		segment := data[offset+2 : offset+segmentLength]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return parseTIFFEXIF(segment[6:])
		}
		offset += segmentLength
	}
	return nil
}

type exifParser struct {
	data     []byte
	order    binary.ByteOrder
	metadata map[string]any
	visited  map[string]bool
}

func parseTIFFEXIF(data []byte) map[string]any {
	if len(data) < 8 {
		return nil
	}
	parser := &exifParser{data: data, metadata: map[string]any{}, visited: map[string]bool{}}
	switch string(data[:2]) {
	case "II":
		parser.order = binary.LittleEndian
	case "MM":
		parser.order = binary.BigEndian
	default:
		return nil
	}
	if parser.u16(2) != 42 {
		return nil
	}
	parser.readIFD(parser.u32(4), "IFD0", 0)
	parser.addGPSDecimals()
	if len(parser.metadata) == 0 {
		return nil
	}
	return parser.metadata
}

func (p *exifParser) u16(offset int) uint16 {
	if offset < 0 || offset+2 > len(p.data) {
		return 0
	}
	return p.order.Uint16(p.data[offset : offset+2])
}

func (p *exifParser) u32(offset int) uint32 {
	if offset < 0 || offset+4 > len(p.data) {
		return 0
	}
	return p.order.Uint32(p.data[offset : offset+4])
}

func (p *exifParser) readIFD(offset uint32, namespace string, depth int) {
	if depth > 3 || offset > uint32(len(p.data)-2) {
		return
	}
	visitKey := fmt.Sprintf("%s:%d", namespace, offset)
	if p.visited[visitKey] {
		return
	}
	p.visited[visitKey] = true
	count := int(p.u16(int(offset)))
	entriesStart := int(offset) + 2
	if count < 0 || count > 4096 || entriesStart > len(p.data) || count > (len(p.data)-entriesStart)/12 {
		return
	}
	for index := 0; index < count; index++ {
		entry := entriesStart + index*12
		tag := p.u16(entry)
		valueType := p.u16(entry + 2)
		valueCount := p.u32(entry + 4)
		value, ok := p.value(entry+8, valueType, valueCount)
		if !ok {
			continue
		}
		key := namespace + "." + exifTagName(namespace, tag)
		p.metadata[key] = value
		switch {
		case namespace == "IFD0" && tag == 0x8769:
			if pointer, ok := firstUint(value); ok {
				p.readIFD(uint32(pointer), "EXIF", depth+1)
			}
		case namespace == "IFD0" && tag == 0x8825:
			if pointer, ok := firstUint(value); ok {
				p.readIFD(uint32(pointer), "GPS", depth+1)
			}
		}
	}
	nextOffset := entriesStart + count*12
	if namespace == "IFD0" && nextOffset+4 <= len(p.data) {
		if next := p.u32(nextOffset); next != 0 {
			p.readIFD(next, "IFD1", depth+1)
		}
	}
}

func (p *exifParser) value(inlineOffset int, valueType uint16, count uint32) (any, bool) {
	width := map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8}[valueType]
	if width == 0 || count == 0 || count > uint32(maxEXIFValueBytes/width) {
		return nil, false
	}
	total := int(count) * width
	valueOffset := inlineOffset
	if total > 4 {
		valueOffset = int(p.u32(inlineOffset))
	}
	if valueOffset < 0 || total > len(p.data)-valueOffset {
		return nil, false
	}
	raw := p.data[valueOffset : valueOffset+total]
	switch valueType {
	case 2:
		return strings.TrimRight(string(raw), "\x00"), true
	case 1, 6, 7:
		if valueType != 7 && isPrintableEXIF(raw) {
			return strings.TrimRight(string(raw), "\x00"), true
		}
		return "hex:" + hex.EncodeToString(raw), true
	case 3:
		return p.unsignedValues(raw, 2), true
	case 4:
		return p.unsignedValues(raw, 4), true
	case 8:
		return p.signedValues(raw, 2), true
	case 9:
		return p.signedValues(raw, 4), true
	case 5:
		return p.rationalValues(raw, false), true
	case 10:
		return p.rationalValues(raw, true), true
	case 11:
		values := make([]any, 0, count)
		for offset := 0; offset+4 <= len(raw); offset += 4 {
			values = append(values, math.Float32frombits(p.order.Uint32(raw[offset:offset+4])))
		}
		return scalarOrArray(values), true
	case 12:
		values := make([]any, 0, count)
		for offset := 0; offset+8 <= len(raw); offset += 8 {
			values = append(values, math.Float64frombits(p.order.Uint64(raw[offset:offset+8])))
		}
		return scalarOrArray(values), true
	default:
		return nil, false
	}
}

func (p *exifParser) unsignedValues(raw []byte, width int) any {
	values := make([]any, 0, len(raw)/width)
	for offset := 0; offset+width <= len(raw); offset += width {
		if width == 2 {
			values = append(values, p.order.Uint16(raw[offset:offset+width]))
		} else {
			values = append(values, p.order.Uint32(raw[offset:offset+width]))
		}
	}
	return scalarOrArray(values)
}

func (p *exifParser) signedValues(raw []byte, width int) any {
	values := make([]any, 0, len(raw)/width)
	for offset := 0; offset+width <= len(raw); offset += width {
		if width == 2 {
			values = append(values, int16(p.order.Uint16(raw[offset:offset+width])))
		} else {
			values = append(values, int32(p.order.Uint32(raw[offset:offset+width])))
		}
	}
	return scalarOrArray(values)
}

func (p *exifParser) rationalValues(raw []byte, signed bool) any {
	values := make([]any, 0, len(raw)/8)
	for offset := 0; offset+8 <= len(raw); offset += 8 {
		var numerator, denominator int64
		if signed {
			numerator = int64(int32(p.order.Uint32(raw[offset : offset+4])))
			denominator = int64(int32(p.order.Uint32(raw[offset+4 : offset+8])))
		} else {
			numerator = int64(p.order.Uint32(raw[offset : offset+4]))
			denominator = int64(p.order.Uint32(raw[offset+4 : offset+8]))
		}
		value := map[string]any{"numerator": numerator, "denominator": denominator}
		if denominator != 0 {
			value["value"] = float64(numerator) / float64(denominator)
		}
		values = append(values, value)
	}
	return scalarOrArray(values)
}

func scalarOrArray(values []any) any {
	if len(values) == 1 {
		return values[0]
	}
	return values
}

func firstUint(value any) (uint64, bool) {
	switch number := value.(type) {
	case uint16:
		return uint64(number), true
	case uint32:
		return uint64(number), true
	case []any:
		if len(number) > 0 {
			return firstUint(number[0])
		}
	}
	return 0, false
}

func (p *exifParser) addGPSDecimals() {
	latitude, latitudeOK := rationalSlice(p.metadata["GPS.GPSLatitude"])
	longitude, longitudeOK := rationalSlice(p.metadata["GPS.GPSLongitude"])
	if latitudeOK {
		if value, ok := gpsDecimal(latitude, fmt.Sprint(p.metadata["GPS.GPSLatitudeRef"])); ok {
			p.metadata["GPS.GPSLatitudeDecimal"] = value
		}
	}
	if longitudeOK {
		if value, ok := gpsDecimal(longitude, fmt.Sprint(p.metadata["GPS.GPSLongitudeRef"])); ok {
			p.metadata["GPS.GPSLongitudeDecimal"] = value
		}
	}
	if altitude, ok := rationalSlice(p.metadata["GPS.GPSAltitude"]); ok && len(altitude) > 0 {
		value := altitude[0]
		if strings.HasPrefix(strings.ToUpper(fmt.Sprint(p.metadata["GPS.GPSAltitudeRef"])), "1") {
			value = -value
		}
		p.metadata["GPS.GPSAltitudeMeters"] = value
	}
}

func rationalSlice(value any) ([]float64, bool) {
	values := []any{value}
	if array, ok := value.([]any); ok {
		values = array
	}
	result := make([]float64, 0, len(values))
	for _, item := range values {
		fraction, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		numerator, numeratorOK := fraction["numerator"].(int64)
		denominator, denominatorOK := fraction["denominator"].(int64)
		if !numeratorOK || !denominatorOK || denominator == 0 {
			return nil, false
		}
		result = append(result, float64(numerator)/float64(denominator))
	}
	return result, len(result) > 0
}

func gpsDecimal(parts []float64, reference string) (float64, bool) {
	if len(parts) < 3 {
		return 0, false
	}
	value := parts[0] + parts[1]/60 + parts[2]/3600
	if strings.EqualFold(reference, "S") || strings.EqualFold(reference, "W") {
		value = -value
	}
	return value, true
}

func isPrintableEXIF(value []byte) bool {
	if len(value) == 0 {
		return false
	}
	for _, character := range value {
		if character != 0 && (character < 0x20 || character > 0x7e) {
			return false
		}
	}
	return true
}

func exifTagName(namespace string, tag uint16) string {
	names := map[string]map[uint16]string{
		"IFD0": {
			0x010e: "ImageDescription", 0x010f: "Make", 0x0110: "Model", 0x0112: "Orientation", 0x011a: "XResolution", 0x011b: "YResolution", 0x0128: "ResolutionUnit", 0x0131: "Software", 0x0132: "DateTime", 0x013b: "Artist", 0x8298: "Copyright", 0x8769: "ExifIFDPointer", 0x8825: "GPSInfoIFDPointer", 0x9c9b: "XPTitle", 0x9c9c: "XPComment", 0x9c9d: "XPAuthor", 0x9c9e: "XPKeywords", 0x9c9f: "XPSubject",
		},
		"IFD1": {
			0x0103: "Compression", 0x0112: "Orientation", 0x0201: "JPEGInterchangeFormat", 0x0202: "JPEGInterchangeFormatLength",
		},
		"EXIF": {
			0x829a: "ExposureTime", 0x829d: "FNumber", 0x8822: "ExposureProgram", 0x8827: "ISOSpeedRatings", 0x8830: "SensitivityType", 0x8832: "PhotographicSensitivity", 0x9000: "ExifVersion", 0x9003: "DateTimeOriginal", 0x9004: "DateTimeDigitized", 0x9010: "OffsetTime", 0x9011: "OffsetTimeOriginal", 0x9101: "ComponentsConfiguration", 0x9102: "CompressedBitsPerPixel", 0x9201: "ShutterSpeedValue", 0x9202: "ApertureValue", 0x9203: "BrightnessValue", 0x9204: "ExposureBiasValue", 0x9205: "MaxApertureValue", 0x9206: "SubjectDistance", 0x9207: "MeteringMode", 0x9209: "Flash", 0x920a: "FocalLength", 0x927c: "MakerNote", 0x9286: "UserComment", 0xa001: "ColorSpace", 0xa002: "PixelXDimension", 0xa003: "PixelYDimension", 0xa004: "RelatedSoundFile", 0xa005: "InteroperabilityIFDPointer", 0xa20e: "FocalPlaneXResolution", 0xa20f: "FocalPlaneYResolution", 0xa210: "FocalPlaneResolutionUnit", 0xa215: "ExposureIndex", 0xa217: "SensingMethod", 0xa300: "FileSource", 0xa301: "SceneType", 0xa401: "CustomRendered", 0xa402: "ExposureMode", 0xa403: "WhiteBalance", 0xa406: "SceneCaptureType", 0xa431: "BodySerialNumber", 0xa432: "LensSpecification", 0xa433: "LensMake", 0xa434: "LensModel", 0xa435: "LensSerialNumber",
		},
		"GPS": {
			0x0000: "GPSVersionID", 0x0001: "GPSLatitudeRef", 0x0002: "GPSLatitude", 0x0003: "GPSLongitudeRef", 0x0004: "GPSLongitude", 0x0005: "GPSAltitudeRef", 0x0006: "GPSAltitude", 0x0007: "GPSTimeStamp", 0x0008: "GPSSatellites", 0x0009: "GPSStatus", 0x000a: "GPSMeasureMode", 0x000b: "GPSDOP", 0x000c: "GPSSpeedRef", 0x000d: "GPSSpeed", 0x000e: "GPSTrackRef", 0x000f: "GPSTrack", 0x0010: "GPSImgDirectionRef", 0x0011: "GPSImgDirection", 0x0012: "GPSMapDatum", 0x001d: "GPSDateStamp", 0x001e: "GPSDifferential",
		},
	}
	if name, ok := names[namespace][tag]; ok {
		return name
	}
	return fmt.Sprintf("0x%04x", tag)
}
