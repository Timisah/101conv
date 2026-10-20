package analysis

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const maxImageMetadataBytes = 16 << 20

// inspectImageMetadata collects metadata which is not reliably exposed by the
// image.Config decoder. The complete bounded APP/chunk/box payloads are kept
// alongside normalized fields so callers can inspect vendor-specific values.
func inspectImageMetadata(path, extension string, fallback []byte) (map[string]any, bool) {
	data, truncated := readMetadataPrefix(path, fallback)
	var metadata map[string]any
	switch extension {
	case "jpg", "jpeg":
		metadata = parseJPEGMetadata(data)
	case "png":
		metadata = parsePNGMetadata(data)
	case "webp":
		metadata = parseWebPMetadata(data)
	case "tif", "tiff", "dng":
		metadata = parseTIFFMetadata(data)
	case "avif", "heic", "heif", "jp2", "j2k", "jpf", "jxl":
		metadata = parseBMFFMetadata(data)
	case "gif":
		metadata = parseGIFMetadata(data)
	case "bmp":
		metadata = parseBMPMetadata(data)
	}
	if len(metadata) == 0 {
		return nil, truncated
	}
	if truncated {
		metadata["_inspection.metadataTruncated"] = true
		metadata["_inspection.metadataBytesScanned"] = len(data)
	}
	return metadata, truncated
}

func readMetadataPrefix(path string, fallback []byte) ([]byte, bool) {
	file, err := os.Open(path)
	if err != nil {
		return fallback, false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxImageMetadataBytes+1))
	if err != nil {
		return fallback, false
	}
	truncated := len(data) > maxImageMetadataBytes
	if truncated {
		data = data[:maxImageMetadataBytes]
	}
	return data, truncated
}

func parseJPEGMetadata(data []byte) map[string]any {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil
	}
	metadata := map[string]any{}
	segments := make([]any, 0)
	comments := make([]string, 0)
	iccChunks := make(map[byte][]byte)
	xmpExtended := make(map[string]map[uint32][]byte)
	for offset := 2; offset+2 <= len(data); {
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
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || offset+length > len(data) {
			break
		}
		payload := data[offset+2 : offset+length]
		segment := map[string]any{
			"marker":    fmt.Sprintf("0x%02x", marker),
			"name":      jpegMarkerName(marker),
			"length":    length - 2,
			"rawBase64": base64.StdEncoding.EncodeToString(payload),
		}
		segments = append(segments, segment)
		switch marker {
		case 0xe0:
			if jfif := parseJFIF(payload); jfif != nil {
				metadata["JPEG.JFIF"] = jfif
			}
		case 0xe1:
			switch {
			case bytes.HasPrefix(payload, []byte("Exif\x00\x00")):
				if exif := parseTIFFEXIF(payload[6:]); len(exif) > 0 {
					metadata["JPEG.EXIF"] = exif
					for key, value := range exif {
						metadata[key] = value
					}
				}
			case bytes.HasPrefix(payload, []byte("http://ns.adobe.com/xap/1.0/\x00")):
				xmp := payload[len("http://ns.adobe.com/xap/1.0/")+1:]
				metadata["JPEG.XMP.Raw"] = string(xmp)
				for key, value := range xmlMetadata(xmp) {
					metadata["JPEG.XMP."+key] = value
				}
			case bytes.HasPrefix(payload, []byte("http://ns.adobe.com/xmp/extension/\x00")):
				guid, total, chunkOffset, chunk, ok := parseExtendedXMP(payload)
				if ok {
					if xmpExtended[guid] == nil {
						xmpExtended[guid] = make(map[uint32][]byte)
					}
					xmpExtended[guid][chunkOffset] = append([]byte(nil), chunk...)
					metadata["JPEG.XMP.ExtendedLength"] = total
				}
			}
		case 0xe2:
			if bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00")) && len(payload) >= 14 {
				sequence := payload[12]
				total := payload[13]
				if sequence > 0 && total > 0 {
					if iccChunks[sequence] == nil {
						iccChunks[sequence] = make([]byte, 0, len(payload)-14)
					}
					iccChunks[sequence] = append(iccChunks[sequence], payload[14:]...)
					metadata["JPEG.ICC.SequenceCount"] = total
				}
			}
		case 0xed:
			if photoshop := parsePhotoshop(payload); photoshop != nil {
				metadata["JPEG.Photoshop"] = photoshop
				if iptc, ok := photoshop["IPTC"].(map[string]any); ok {
					metadata["JPEG.IPTC"] = iptc
				}
			}
		case 0xee:
			if adobe := parseAdobe(payload); adobe != nil {
				metadata["JPEG.Adobe"] = adobe
			}
		case 0xfe:
			comments = append(comments, string(payload))
		}
		offset += length
	}
	if len(iccChunks) > 0 {
		profile := make([]byte, 0)
		keys := make([]int, 0, len(iccChunks))
		for sequence := range iccChunks {
			keys = append(keys, int(sequence))
		}
		sort.Ints(keys)
		for _, sequence := range keys {
			profile = append(profile, iccChunks[byte(sequence)]...)
		}
		metadata["JPEG.ICCProfile"] = parseICCProfile(profile)
	}
	for guid, chunks := range xmpExtended {
		keys := make([]int, 0, len(chunks))
		for offset := range chunks {
			keys = append(keys, int(offset))
		}
		sort.Ints(keys)
		var raw []byte
		for _, offset := range keys {
			raw = append(raw, chunks[uint32(offset)]...)
		}
		metadata["JPEG.XMP.Extended."+guid+".Raw"] = string(raw)
		for key, value := range xmlMetadata(raw) {
			metadata["JPEG.XMP.Extended."+guid+"."+key] = value
		}
	}
	if len(comments) > 0 {
		metadata["JPEG.Comments"] = comments
	}
	if len(segments) > 0 {
		metadata["JPEG.APPSegments"] = segments
	}
	return metadata
}

func jpegMarkerName(marker byte) string {
	switch {
	case marker >= 0xe0 && marker <= 0xef:
		return fmt.Sprintf("APP%d", marker-0xe0)
	case marker == 0xfe:
		return "COM"
	default:
		return fmt.Sprintf("0x%02x", marker)
	}
}

func parseJFIF(data []byte) map[string]any {
	if !bytes.HasPrefix(data, []byte("JFIF\x00")) || len(data) < 14 {
		return nil
	}
	result := map[string]any{
		"version":         fmt.Sprintf("%d.%02d", data[5], data[6]),
		"densityUnits":    data[7],
		"xDensity":        binary.BigEndian.Uint16(data[8:10]),
		"yDensity":        binary.BigEndian.Uint16(data[10:12]),
		"thumbnailWidth":  data[12],
		"thumbnailHeight": data[13],
	}
	thumbnailBytes := int(data[12]) * int(data[13]) * 3
	if thumbnailBytes > 0 && 14+thumbnailBytes <= len(data) {
		result["thumbnailBase64"] = base64.StdEncoding.EncodeToString(data[14 : 14+thumbnailBytes])
	}
	return result
}

func parseExtendedXMP(data []byte) (string, uint32, uint32, []byte, bool) {
	prefix := []byte("http://ns.adobe.com/xmp/extension/\x00")
	if !bytes.HasPrefix(data, prefix) || len(data) < len(prefix)+40 {
		return "", 0, 0, nil, false
	}
	start := len(prefix)
	guid := string(data[start : start+32])
	total := binary.BigEndian.Uint32(data[start+32 : start+36])
	offset := binary.BigEndian.Uint32(data[start+36 : start+40])
	chunk := data[start+40:]
	if uint64(offset)+uint64(len(chunk)) > uint64(total) {
		return "", 0, 0, nil, false
	}
	return guid, total, offset, chunk, true
}

func parseAdobe(data []byte) map[string]any {
	if !bytes.HasPrefix(data, []byte("Adobe")) || len(data) < 15 {
		return nil
	}
	return map[string]any{
		"version": binary.BigEndian.Uint16(data[5:7]),
		"flags0":  binary.BigEndian.Uint32(data[7:11]),
		"flags1":  binary.BigEndian.Uint32(data[11:15]),
	}
}

func parsePhotoshop(data []byte) map[string]any {
	if !bytes.HasPrefix(data, []byte("Photoshop 3.0\x00")) {
		return nil
	}
	result := map[string]any{"rawBase64": base64.StdEncoding.EncodeToString(data)}
	resources := make([]any, 0)
	iptc := map[string]any{}
	offset := len("Photoshop 3.0\x00")
	for offset+12 <= len(data) {
		if string(data[offset:offset+4]) != "8BIM" {
			break
		}
		resourceID := binary.BigEndian.Uint16(data[offset+4 : offset+6])
		nameLength := int(data[offset+6])
		nameStart := offset + 7
		nameEnd := nameStart + nameLength
		if nameEnd > len(data) {
			break
		}
		name := string(data[nameStart:nameEnd])
		cursor := nameEnd
		if (cursor-nameStart)%2 != 0 {
			cursor++
		}
		if cursor+4 > len(data) {
			break
		}
		size := int(binary.BigEndian.Uint32(data[cursor : cursor+4]))
		cursor += 4
		if size < 0 || size > len(data)-cursor {
			break
		}
		resourceData := data[cursor : cursor+size]
		resource := map[string]any{"id": fmt.Sprintf("0x%04x", resourceID), "name": name, "length": size, "rawBase64": base64.StdEncoding.EncodeToString(resourceData)}
		if resourceID == 0x0404 {
			for key, value := range parseIPTC(resourceData) {
				iptc[key] = value
			}
		}
		resources = append(resources, resource)
		offset = cursor + size
		if size%2 != 0 {
			offset++
		}
	}
	if len(resources) > 0 {
		result["IRBResources"] = resources
	}
	if len(iptc) > 0 {
		result["IPTC"] = iptc
	}
	return result
}

func parseIPTC(data []byte) map[string]any {
	result := map[string]any{}
	for offset := 0; offset+5 <= len(data); {
		if data[offset] != 0x1c {
			offset++
			continue
		}
		record, dataset := data[offset+1], data[offset+2]
		length := int(binary.BigEndian.Uint16(data[offset+3 : offset+5]))
		offset += 5
		if length&0x8000 != 0 {
			bytesOfLength := length & 0x7fff
			if bytesOfLength <= 0 || bytesOfLength > 4 || offset+bytesOfLength > len(data) {
				break
			}
			length = 0
			for _, value := range data[offset : offset+bytesOfLength] {
				length = length<<8 | int(value)
			}
			offset += bytesOfLength
		}
		if length < 0 || length > len(data)-offset {
			break
		}
		key := fmt.Sprintf("%d:%d:%s", record, dataset, iptcDatasetName(record, dataset))
		value := string(data[offset : offset+length])
		if existing, ok := result[key]; ok {
			if values, ok := existing.([]any); ok {
				result[key] = append(values, value)
			} else {
				result[key] = []any{existing, value}
			}
		} else {
			result[key] = value
		}
		offset += length
	}
	return result
}

func iptcDatasetName(record, dataset byte) string {
	names := map[[2]byte]string{
		{2, 5}: "ObjectName", {2, 7}: "EditStatus", {2, 10}: "Urgency", {2, 15}: "Category", {2, 20}: "SupplementalCategories", {2, 25}: "Keywords", {2, 40}: "SpecialInstructions", {2, 55}: "DateCreated", {2, 60}: "TimeCreated", {2, 80}: "Byline", {2, 85}: "BylineTitle", {2, 90}: "City", {2, 92}: "SubLocation", {2, 95}: "ProvinceState", {2, 100}: "CountryCode", {2, 101}: "CountryName", {2, 105}: "Headline", {2, 110}: "Credit", {2, 115}: "Source", {2, 116}: "CopyrightNotice", {2, 120}: "CaptionAbstract", {2, 122}: "WriterEditor", {2, 130}: "ImageType", {2, 135}: "LanguageIdentifier", {2, 150}: "AudioType", {2, 151}: "AudioSamplingRate", {2, 152}: "AudioSampleType", {2, 153}: "AudioChannels", {2, 200}: "LocationName", {2, 202}: "WorldRegion", {2, 221}: "LocationShown"}
	if name, ok := names[[2]byte{record, dataset}]; ok {
		return name
	}
	return "Unknown"
}

func parsePNGMetadata(data []byte) map[string]any {
	if len(data) < 24 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil
	}
	metadata := map[string]any{}
	chunks := make([]any, 0)
	textValues := map[string]any{}
	for offset := 8; offset+12 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if length < 0 || length > len(data)-offset-12 {
			break
		}
		kind := string(data[offset+4 : offset+8])
		payload := data[offset+8 : offset+8+length]
		chunk := map[string]any{"type": kind, "length": length, "crc": fmt.Sprintf("%08x", binary.BigEndian.Uint32(data[offset+8+length:offset+12+length]))}
		if kind != "IDAT" && kind != "fdAT" {
			chunk["rawBase64"] = base64.StdEncoding.EncodeToString(payload)
			chunks = append(chunks, chunk)
		}
		switch kind {
		case "tEXt":
			if key, value, ok := splitNUL(payload); ok {
				textValues["tEXt."+key] = value
			}
		case "zTXt":
			if key, compressed, ok := splitPNGText(payload); ok {
				if value, err := inflateBounded(compressed); err == nil {
					textValues["zTXt."+key] = string(value)
				}
			}
		case "iTXt":
			if key, value, ok := parsePNGInternationalText(payload); ok {
				textValues["iTXt."+key] = value
			}
		case "eXIf":
			if exif := parseTIFFEXIF(payload); len(exif) > 0 {
				metadata["PNG.EXIF"] = exif
				for key, value := range exif {
					metadata[key] = value
				}
			}
		case "iCCP":
			if profile := parsePNGICC(payload); profile != nil {
				metadata["PNG.ICCProfile"] = profile
			}
		case "pHYs":
			if len(payload) >= 9 {
				metadata["PNG.PhysicalPixelDimensions"] = map[string]any{"pixelsPerUnitX": binary.BigEndian.Uint32(payload[:4]), "pixelsPerUnitY": binary.BigEndian.Uint32(payload[4:8]), "unit": payload[8]}
			}
		case "gAMA":
			if len(payload) >= 4 {
				metadata["PNG.Gamma"] = float64(binary.BigEndian.Uint32(payload[:4])) / 100000
			}
		case "sRGB":
			if len(payload) > 0 {
				metadata["PNG.RenderingIntent"] = payload[0]
			}
		case "cHRM":
			if len(payload) >= 32 {
				values := make([]float64, 0, 8)
				for index := 0; index < 8; index++ {
					values = append(values, float64(binary.BigEndian.Uint32(payload[index*4:index*4+4]))/100000)
				}
				metadata["PNG.Chromaticities"] = values
			}
		case "tIME":
			if len(payload) >= 7 {
				metadata["PNG.ModifiedTime"] = fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02dZ", binary.BigEndian.Uint16(payload[:2]), payload[2], payload[3], payload[4], payload[5], payload[6])
			}
		case "acTL":
			if len(payload) >= 8 {
				metadata["PNG.Animation"] = map[string]any{"frames": binary.BigEndian.Uint32(payload[:4]), "plays": binary.BigEndian.Uint32(payload[4:8])}
			}
		}
		offset += 12 + length
		if kind == "IEND" {
			break
		}
	}
	if len(chunks) > 0 {
		metadata["PNG.Chunks"] = chunks
	}
	if len(textValues) > 0 {
		metadata["PNG.Text"] = textValues
	}
	return metadata
}

func splitNUL(data []byte) (string, string, bool) {
	index := bytes.IndexByte(data, 0)
	if index <= 0 {
		return "", "", false
	}
	return string(data[:index]), string(data[index+1:]), true
}

func splitPNGText(data []byte) (string, []byte, bool) {
	index := bytes.IndexByte(data, 0)
	if index <= 0 || index+2 > len(data) || data[index+1] != 0 {
		return "", nil, false
	}
	return string(data[:index]), data[index+2:], true
}

func parsePNGInternationalText(data []byte) (string, string, bool) {
	parts := bytes.SplitN(data, []byte{0}, 6)
	if len(parts) != 6 {
		return "", "", false
	}
	value := parts[5]
	if len(parts[1]) > 0 && parts[1][0] == 1 {
		inflated, err := inflateBounded(value)
		if err != nil {
			return "", "", false
		}
		value = inflated
	}
	return string(parts[0]), string(value), true
}

func inflateBounded(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, maxImageMetadataBytes))
}

func parsePNGICC(data []byte) map[string]any {
	nameEnd := bytes.IndexByte(data, 0)
	if nameEnd <= 0 || nameEnd+2 > len(data) || data[nameEnd+1] != 0 {
		return nil
	}
	profile, err := inflateBounded(data[nameEnd+2:])
	if err != nil {
		return nil
	}
	result := parseICCProfile(profile)
	result["name"] = string(data[:nameEnd])
	return result
}

func parseWebPMetadata(data []byte) map[string]any {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil
	}
	metadata := map[string]any{}
	chunks := make([]any, 0)
	for offset := 12; offset+8 <= len(data); {
		kind := string(data[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if length < 0 || length > len(data)-offset-8 {
			break
		}
		payload := data[offset+8 : offset+8+length]
		chunk := map[string]any{"type": kind, "length": length}
		if kind != "VP8 " && kind != "VP8L" && kind != "ALPH" && kind != "ANMF" {
			chunk["rawBase64"] = base64.StdEncoding.EncodeToString(payload)
		}
		chunks = append(chunks, chunk)
		switch kind {
		case "EXIF":
			if exif := parseTIFFEXIF(bytes.TrimPrefix(payload, []byte("Exif\x00\x00"))); len(exif) > 0 {
				metadata["WebP.EXIF"] = exif
				for key, value := range exif {
					metadata[key] = value
				}
			}
		case "XMP ":
			metadata["WebP.XMP.Raw"] = string(payload)
			for key, value := range xmlMetadata(payload) {
				metadata["WebP.XMP."+key] = value
			}
		case "ICCP":
			metadata["WebP.ICCProfile"] = parseICCProfile(payload)
		case "VP8X":
			if len(payload) >= 10 {
				metadata["WebP.Features"] = map[string]any{"animation": payload[1]&0x02 != 0, "xmp": payload[1]&0x04 != 0, "exif": payload[1]&0x08 != 0, "alpha": payload[1]&0x10 != 0, "width": 1 + uint24LE(payload[4:7]), "height": 1 + uint24LE(payload[7:10])}
			}
		case "ANIM":
			if len(payload) >= 6 {
				metadata["WebP.Animation"] = map[string]any{"backgroundColor": fmt.Sprintf("#%02x%02x%02x%02x", payload[0], payload[1], payload[2], payload[3]), "loopCount": binary.LittleEndian.Uint16(payload[4:6])}
			}
		}
		offset += 8 + length
		if length%2 != 0 {
			offset++
		}
	}
	if len(chunks) > 0 {
		metadata["WebP.Chunks"] = chunks
	}
	return metadata
}

func uint24LE(data []byte) int {
	if len(data) < 3 {
		return 0
	}
	return int(data[0]) | int(data[1])<<8 | int(data[2])<<16
}

func parseTIFFMetadata(data []byte) map[string]any {
	metadata := parseTIFFEXIF(data)
	if len(metadata) == 0 {
		return nil
	}
	return map[string]any{"TIFF.Tags": metadata}
}

func parseGIFMetadata(data []byte) map[string]any {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return nil
	}
	metadata := map[string]any{"GIF.LogicalScreen": map[string]any{"width": binary.LittleEndian.Uint16(data[6:8]), "height": binary.LittleEndian.Uint16(data[8:10]), "backgroundColorIndex": data[11], "pixelAspectRatio": data[12]}}
	comments := make([]string, 0)
	for offset := 13; offset < len(data); {
		if data[offset] == 0x21 && offset+2 < len(data) {
			switch data[offset+1] {
			case 0xfe:
				value, next := readGIFSubBlocks(data, offset+2)
				comments = append(comments, string(value))
				offset = next
				continue
			case 0xff:
				if offset+3 <= len(data) {
					value, next := readGIFSubBlocks(data, offset+3)
					metadata["GIF.ApplicationExtension"] = map[string]any{"identifier": string(value), "rawBase64": base64.StdEncoding.EncodeToString(value)}
					offset = next
					continue
				}
			}
		}
		offset++
	}
	if len(comments) > 0 {
		metadata["GIF.Comments"] = comments
	}
	return metadata
}

func readGIFSubBlocks(data []byte, offset int) ([]byte, int) {
	var result []byte
	for offset < len(data) {
		length := int(data[offset])
		offset++
		if length == 0 || length > len(data)-offset {
			break
		}
		result = append(result, data[offset:offset+length]...)
		offset += length
	}
	return result, offset
}

func parseBMPMetadata(data []byte) map[string]any {
	if len(data) < 26 || string(data[:2]) != "BM" {
		return nil
	}
	result := map[string]any{"BMP.FileHeader": map[string]any{"fileSize": binary.LittleEndian.Uint32(data[2:6]), "pixelOffset": binary.LittleEndian.Uint32(data[10:14])}}
	if len(data) >= 54 {
		result["BMP.DIBHeader"] = map[string]any{"size": binary.LittleEndian.Uint32(data[14:18]), "width": int32(binary.LittleEndian.Uint32(data[18:22])), "height": int32(binary.LittleEndian.Uint32(data[22:26])), "planes": binary.LittleEndian.Uint16(data[26:28]), "bitsPerPixel": binary.LittleEndian.Uint16(data[28:30]), "compression": binary.LittleEndian.Uint32(data[30:34]), "xPixelsPerMeter": int32(binary.LittleEndian.Uint32(data[38:42])), "yPixelsPerMeter": int32(binary.LittleEndian.Uint32(data[42:46])), "colorsUsed": binary.LittleEndian.Uint32(data[46:50]), "importantColors": binary.LittleEndian.Uint32(data[50:54])}
	}
	return result
}

func parseICCProfile(data []byte) map[string]any {
	if len(data) < 132 {
		return map[string]any{"length": len(data), "rawBase64": base64.StdEncoding.EncodeToString(data)}
	}
	result := map[string]any{"length": len(data), "profileSize": binary.BigEndian.Uint32(data[:4]), "preferredCMM": string(data[4:8]), "version": fmt.Sprintf("%d.%d.%d", data[8], data[9], data[10]>>4), "deviceClass": string(data[12:16]), "colorSpace": string(data[16:20]), "PCS": string(data[20:24]), "manufacturer": string(data[48:52]), "model": string(data[52:56]), "renderingIntent": binary.BigEndian.Uint32(data[64:68]), "rawBase64": base64.StdEncoding.EncodeToString(data)}
	tagCount := int(binary.BigEndian.Uint32(data[128:132]))
	tags := make([]any, 0, tagCount)
	for index := 0; index < tagCount && 132+index*12+12 <= len(data); index++ {
		offset := 132 + index*12
		tagOffset := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		tagSize := int(binary.BigEndian.Uint32(data[offset+8 : offset+12]))
		tag := map[string]any{"signature": string(data[offset : offset+4]), "offset": tagOffset, "size": tagSize}
		if tagOffset >= 0 && tagSize >= 0 && tagSize <= len(data)-tagOffset {
			tag["type"] = string(data[tagOffset : tagOffset+4])
			tag["rawBase64"] = base64.StdEncoding.EncodeToString(data[tagOffset : tagOffset+tagSize])
		}
		tags = append(tags, tag)
	}
	if len(tags) > 0 {
		result["tags"] = tags
	}
	return result
}

func parseBMFFMetadata(data []byte) map[string]any {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return nil
	}
	metadata := map[string]any{}
	boxes := make([]any, 0)
	walkBMFFBoxes(data, 0, len(data), 0, &boxes, metadata)
	if len(boxes) > 0 {
		metadata["BMFF.Boxes"] = boxes
	}
	return metadata
}

func walkBMFFBoxes(data []byte, start, end, depth int, boxes *[]any, metadata map[string]any) {
	if depth > 8 {
		return
	}
	for offset := start; offset+8 <= end && offset+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		headerSize := 8
		if size == 1 {
			if offset+16 > end {
				return
			}
			size = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(end - offset)
		}
		if size < uint64(headerSize) || size > uint64(end-offset) {
			return
		}
		kind := string(data[offset+4 : offset+8])
		payloadStart := offset + headerSize
		payloadEnd := offset + int(size)
		box := map[string]any{"type": kind, "offset": offset, "size": size}
		if kind == "ftyp" && payloadEnd >= payloadStart+8 {
			brands := make([]string, 0)
			for cursor := payloadStart + 8; cursor+4 <= payloadEnd; cursor += 4 {
				brands = append(brands, string(data[cursor:cursor+4]))
			}
			metadata["BMFF.FileType"] = map[string]any{"majorBrand": string(data[payloadStart : payloadStart+4]), "minorVersion": binary.BigEndian.Uint32(data[payloadStart+4 : payloadStart+8]), "compatibleBrands": brands}
		}
		if kind == "Exif" || kind == "xml " || kind == "uuid" || kind == "colr" || kind == "pasp" || kind == "ispe" || kind == "irot" || kind == "imir" {
			box["rawBase64"] = base64.StdEncoding.EncodeToString(data[payloadStart:payloadEnd])
		}
		if kind == "Exif" {
			exifData := data[payloadStart:payloadEnd]
			exif := parseTIFFEXIF(exifData)
			if len(exif) == 0 && len(exifData) >= 4 {
				exif = parseTIFFEXIF(exifData[4:])
			}
			if len(exif) > 0 {
				metadata["BMFF.EXIF"] = exif
				for key, value := range exif {
					metadata[key] = value
				}
			}
		}
		if kind == "xml " {
			metadata["BMFF.XMP.Raw"] = string(data[payloadStart:payloadEnd])
			for key, value := range xmlMetadata(data[payloadStart:payloadEnd]) {
				metadata["BMFF.XMP."+key] = value
			}
		}
		*boxes = append(*boxes, box)
		if bmffContainer(kind) {
			childStart := payloadStart
			if kind == "meta" && childStart+4 <= payloadEnd {
				childStart += 4
			}
			walkBMFFBoxes(data, childStart, payloadEnd, depth+1, boxes, metadata)
		}
		offset += int(size)
	}
}

func bmffContainer(kind string) bool {
	switch kind {
	case "moov", "trak", "mdia", "minf", "stbl", "edts", "dinf", "mvex", "moof", "traf", "mfra", "udta", "meta", "iprp", "ipco", "iinf":
		return true
	default:
		return false
	}
}

func xmlMetadata(data []byte) map[string]string {
	decoder := xml.NewDecoder(io.LimitReader(bytes.NewReader(data), maxImageMetadataBytes))
	metadata := map[string]string{}
	stack := make([]string, 0)
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
			for _, attribute := range value.Attr {
				metadata[strings.Join(stack, ".")+".@"+attribute.Name.Local] = attribute.Value
			}
			text.Reset()
		case xml.CharData:
			if len(stack) > 0 {
				text.Write([]byte(value))
			}
		case xml.EndElement:
			if len(stack) > 0 {
				if valueText := strings.TrimSpace(text.String()); valueText != "" {
					metadata[strings.Join(stack, ".")] = valueText
				}
				stack = stack[:len(stack)-1]
				text.Reset()
			}
		}
	}
	return metadata
}
