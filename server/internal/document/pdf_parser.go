package document

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxPDFParseBytes = 128 << 20 // 128 MiB bounded parser ceiling
	maxPDFObjects    = 50000
	maxPDFDepth      = 64
	maxObjStmBytes   = 16 << 20
)

var (
	ErrEncryptedPDF = errors.New("encrypted PDFs are not supported")
	ErrInvalidPDF   = errors.New("invalid or corrupted PDF document")
)

type PDFPageInfo struct {
	PageNumber int     `json:"pageNumber"`
	WidthPt    float64 `json:"widthPt"`
	HeightPt   float64 `json:"heightPt"`
	Rotation   int     `json:"rotation"`
}

type pdfIndirectRef struct {
	Num int
	Gen int
}

type pdfObject struct {
	Raw    []byte // raw bytes between "N G obj" and "endobj"
	Dict   []byte // dictionary slice if object starts with "<<"
	Stream []byte // stream bytes if present (raw encoded bytes)
	HasStr bool
}

type pdfPageNode struct {
	ObjNum    int
	Dict      []byte
	MediaBox  [4]float64
	HasBox    bool
	Rotate    int
	Resources []byte // inherited /Resources value if page itself lacks /Resources
}

type ParsedPDF struct {
	data    []byte
	objects map[int]pdfObject
	rootNum int
	pages   []pdfPageNode
}

var (
	objHeaderRE    = regexp.MustCompile(`(?m)(\d+)\s+(\d+)\s+obj\b`)
	indirectRefRE  = regexp.MustCompile(`(\d+)\s+(\d+)\s+R\b`)
	startxrefRE    = regexp.MustCompile(`startxref\s+(\d+)`)
	trailerRootRE  = regexp.MustCompile(`/Root\s+(\d+)\s+(\d+)\s+R`)
	trailerEncRE   = regexp.MustCompile(`/Encrypt\b`)
	numberTokenRE  = regexp.MustCompile(`[-+]?(?:\d+\.?\d*|\.\d+)`)
)

// ParsePDF parses a PDF byte slice into an indexed object table and ordered
// page tree, enforcing strict size, object, and recursion bounds.
func ParsePDF(data []byte) (*ParsedPDF, error) {
	if len(data) < 16 || len(data) > maxPDFParseBytes {
		return nil, fmt.Errorf("%w: file size out of bounds", ErrInvalidPDF)
	}
	headerIdx := bytes.Index(data[:minInt(len(data), 1024)], []byte("%PDF-"))
	if headerIdx < 0 {
		return nil, fmt.Errorf("%w: missing %%PDF header", ErrInvalidPDF)
	}
	if headerIdx > 0 {
		data = data[headerIdx:]
	}

	p := &ParsedPDF{
		data:    data,
		objects: make(map[int]pdfObject),
	}

	if err := p.indexTopLevelObjects(); err != nil {
		return nil, err
	}
	if err := p.expandObjectStreams(); err != nil {
		return nil, err
	}
	if err := p.findRootAndCheckEncryption(); err != nil {
		return nil, err
	}
	if err := p.collectPages(); err != nil {
		return nil, err
	}
	if len(p.pages) == 0 {
		return nil, fmt.Errorf("%w: no pages found", ErrInvalidPDF)
	}
	return p, nil
}

func (p *ParsedPDF) PageCount() int {
	return len(p.pages)
}

func (p *ParsedPDF) PageInfos() []PDFPageInfo {
	out := make([]PDFPageInfo, len(p.pages))
	for i, pg := range p.pages {
		w := math.Abs(pg.MediaBox[2] - pg.MediaBox[0])
		h := math.Abs(pg.MediaBox[3] - pg.MediaBox[1])
		if w <= 0 || h <= 0 {
			w, h = A4WidthMM*PointsPerMM, A4HeightMM*PointsPerMM
		}
		rot := ((pg.Rotate % 360) + 360) % 360
		if rot == 90 || rot == 270 {
			w, h = h, w
		}
		out[i] = PDFPageInfo{
			PageNumber: i + 1,
			WidthPt:    math.Round(w*100) / 100,
			HeightPt:   math.Round(h*100) / 100,
			Rotation:   rot,
		}
	}
	return out
}

// indexTopLevelObjects scans top-level indirect objects in the PDF file.
// Scanning in file order naturally respects incremental updates because later
// definitions of the same object number overwrite earlier ones.
func (p *ParsedPDF) indexTopLevelObjects() error {
	pos := 0
	n := len(p.data)
	count := 0
	for pos < n {
		loc := objHeaderRE.FindIndex(p.data[pos:])
		if loc == nil {
			break
		}
		absStart := pos + loc[0]
		absBodyStart := pos + loc[1]
		// Ensure the match is at a token boundary (start of file or whitespace).
		if absStart > 0 && !isPDFWhitespace(p.data[absStart-1]) {
			pos = absBodyStart
			continue
		}
		sub := objHeaderRE.FindSubmatch(p.data[absStart:absBodyStart])
		objNum, err := strconv.Atoi(string(sub[1]))
		if err != nil || objNum <= 0 {
			pos = absBodyStart
			continue
		}

		// Find matching endobj, skipping over stream payloads cleanly.
		bodyEnd, nextPos, ok := findEndObj(p.data, absBodyStart)
		if !ok {
			pos = absBodyStart
			continue
		}
		raw := bytes.TrimSpace(p.data[absBodyStart:bodyEnd])
		obj := parseRawObject(raw)
		p.objects[objNum] = obj
		count++
		if count > maxPDFObjects {
			return fmt.Errorf("%w: exceeds maximum object limit (%d)", ErrInvalidPDF, maxPDFObjects)
		}
		pos = nextPos
	}
	if len(p.objects) == 0 {
		return fmt.Errorf("%w: no indirect objects found", ErrInvalidPDF)
	}
	return nil
}

func findEndObj(data []byte, start int) (bodyEnd int, nextPos int, ok bool) {
	// Skip leading whitespace.
	i := start
	for i < len(data) && isPDFWhitespace(data[i]) {
		i++
	}
	// If this object begins with a dictionary, find its matching ">>" to see if
	// a "stream" keyword immediately follows.
	searchFrom := i
	if i+1 < len(data) && data[i] == '<' && data[i+1] == '<' {
		if dictEnd := findMatchingDictEnd(data, i); dictEnd > i {
			dict := data[i:dictEnd]
			j := dictEnd
			for j < len(data) && isPDFWhitespace(data[j]) {
				j++
			}
			if bytes.HasPrefix(data[j:], []byte("stream")) {
				k := j + len("stream")
				if k < len(data) && data[k] == '\r' && k+1 < len(data) && data[k+1] == '\n' {
					k += 2
				} else if k < len(data) && (data[k] == '\n' || data[k] == '\r') {
					k++
				}
				// If /Length is a direct integer, jump right over the binary stream!
				if length, hasDirectLen := extractDirectInt(dict, "/Length"); hasDirectLen && length >= 0 && k+length <= len(data) {
					searchFrom = k + length
				} else {
					if endStr := bytes.Index(data[k:], []byte("endstream")); endStr >= 0 {
						searchFrom = k + endStr + len("endstream")
					}
				}
			} else {
				searchFrom = dictEnd
			}
		}
	}
	idx := bytes.Index(data[searchFrom:], []byte("endobj"))
	if idx < 0 {
		return 0, 0, false
	}
	end := searchFrom + idx
	return end, end + len("endobj"), true
}

func parseRawObject(raw []byte) pdfObject {
	obj := pdfObject{Raw: raw}
	if len(raw) >= 2 && raw[0] == '<' && raw[1] == '<' {
		if end := findMatchingDictEnd(raw, 0); end > 0 {
			obj.Dict = raw[:end]
			rest := bytes.TrimLeft(raw[end:], " \t\r\n")
			if bytes.HasPrefix(rest, []byte("stream")) {
				s := rest[len("stream"):]
				if bytes.HasPrefix(s, []byte("\r\n")) {
					s = s[2:]
				} else if len(s) > 0 && (s[0] == '\n' || s[0] == '\r') {
					s = s[1:]
				}
				if endIdx := bytes.LastIndex(s, []byte("endstream")); endIdx >= 0 {
					payload := s[:endIdx]
					payload = bytes.TrimSuffix(payload, []byte("\r\n"))
					payload = bytes.TrimSuffix(payload, []byte("\n"))
					obj.Stream = payload
					obj.HasStr = true
				}
			}
		}
	}
	return obj
}

// expandObjectStreams inflates PDF 1.5+ /Type /ObjStm compressed object streams
// so compressed page/catalog/resource objects are transparently available.
func (p *ParsedPDF) expandObjectStreams() error {
	var streamObjNums []int
	for num, obj := range p.objects {
		if obj.HasStr && len(obj.Dict) > 0 && hasNameValue(obj.Dict, "/Type", "/ObjStm") {
			streamObjNums = append(streamObjNums, num)
		}
	}
	for _, num := range streamObjNums {
		obj := p.objects[num]
		nVal, okN := extractDirectInt(obj.Dict, "/N")
		firstVal, okFirst := extractDirectInt(obj.Dict, "/First")
		if !okN || !okFirst || nVal <= 0 || firstVal < 0 {
			continue
		}
		decoded, err := decodeStream(obj.Dict, obj.Stream)
		if err != nil || firstVal > len(decoded) {
			continue
		}
		header := string(decoded[:firstVal])
		fields := strings.Fields(header)
		if len(fields) < nVal*2 {
			continue
		}
		nums := make([]int, nVal)
		offsets := make([]int, nVal)
		valid := true
		for i := 0; i < nVal; i++ {
			id, err1 := strconv.Atoi(fields[i*2])
			off, err2 := strconv.Atoi(fields[i*2+1])
			if err1 != nil || err2 != nil || id <= 0 || off < 0 {
				valid = false
				break
			}
			nums[i] = id
			offsets[i] = off
		}
		if !valid {
			continue
		}
		body := decoded[firstVal:]
		for i := 0; i < nVal; i++ {
			start := offsets[i]
			end := len(body)
			if i+1 < nVal {
				end = offsets[i+1]
			}
			if start > len(body) || end > len(body) || end < start {
				continue
			}
			if _, exists := p.objects[nums[i]]; !exists {
				raw := bytes.TrimSpace(body[start:end])
				p.objects[nums[i]] = parseRawObject(raw)
				if len(p.objects) > maxPDFObjects {
					return fmt.Errorf("%w: exceeds maximum object limit", ErrInvalidPDF)
				}
			}
		}
	}
	return nil
}

func decodeStream(dict, stream []byte) ([]byte, error) {
	if !bytes.Contains(dict, []byte("/FlateDecode")) {
		return stream, nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(stream))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxObjStmBytes+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxObjStmBytes {
		return nil, fmt.Errorf("object stream exceeds size bound")
	}
	return out, nil
}

func (p *ParsedPDF) findRootAndCheckEncryption() error {
	// Check trailers and XRef stream dictionaries for /Encrypt and /Root.
	// Search from the end of the file to respect incremental updates.
	tailStart := maxInt(0, len(p.data)-65536)
	tail := p.data[tailStart:]

	// Check XRef stream objects first (PDF 1.5+).
	for _, obj := range p.objects {
		if len(obj.Dict) > 0 && hasNameValue(obj.Dict, "/Type", "/XRef") {
			if trailerEncRE.Match(obj.Dict) {
				return ErrEncryptedPDF
			}
			if m := trailerRootRE.FindSubmatch(obj.Dict); m != nil {
				if rootNum, err := strconv.Atoi(string(m[1])); err == nil && rootNum > 0 {
					p.rootNum = rootNum
				}
			}
		}
	}

	// Check classic trailer(s).
	if idx := bytes.LastIndex(tail, []byte("trailer")); idx >= 0 {
		trailerSection := tail[idx:]
		if trailerEncRE.Match(trailerSection) {
			return ErrEncryptedPDF
		}
		if m := trailerRootRE.FindSubmatch(trailerSection); m != nil {
			if rootNum, err := strconv.Atoi(string(m[1])); err == nil && rootNum > 0 {
				p.rootNum = rootNum
			}
		}
	}

	// Fallback: find the /Type /Catalog object directly.
	if p.rootNum == 0 {
		for num, obj := range p.objects {
			if len(obj.Dict) > 0 && hasNameValue(obj.Dict, "/Type", "/Catalog") {
				p.rootNum = num
				break
			}
		}
	}
	if p.rootNum == 0 {
		return fmt.Errorf("%w: missing PDF catalog", ErrInvalidPDF)
	}
	return nil
}

func (p *ParsedPDF) collectPages() error {
	catalog, ok := p.objects[p.rootNum]
	if !ok || len(catalog.Dict) == 0 {
		return fmt.Errorf("%w: invalid catalog object", ErrInvalidPDF)
	}
	pagesRef, ok := extractRef(catalog.Dict, "/Pages")
	if !ok {
		return fmt.Errorf("%w: catalog missing /Pages", ErrInvalidPDF)
	}
	visited := make(map[int]bool)
	return p.walkPagesTree(pagesRef.Num, [4]float64{}, false, 0, nil, visited, 0)
}

func (p *ParsedPDF) walkPagesTree(objNum int, inhBox [4]float64, hasInhBox bool, inhRotate int, inhResources []byte, visited map[int]bool, depth int) error {
	if depth > maxPDFDepth {
		return fmt.Errorf("%w: page tree nesting too deep", ErrInvalidPDF)
	}
	if visited[objNum] {
		return fmt.Errorf("%w: cycle in page tree", ErrInvalidPDF)
	}
	visited[objNum] = true
	obj, ok := p.objects[objNum]
	if !ok || len(obj.Dict) == 0 {
		return nil
	}

	box := inhBox
	hasBox := hasInhBox
	if b, okBox := p.extractRect(obj.Dict, "/MediaBox"); okBox {
		box = b
		hasBox = true
	}
	rotate := inhRotate
	if r, okRot := extractDirectInt(obj.Dict, "/Rotate"); okRot {
		rotate = r
	}
	resources := inhResources
	if res := extractDictEntryValue(obj.Dict, "/Resources"); len(res) > 0 {
		resources = res
	}

	if hasNameValue(obj.Dict, "/Type", "/Pages") || bytes.Contains(obj.Dict, []byte("/Kids")) {
		kids := p.extractKids(obj.Dict)
		for _, kidNum := range kids {
			if err := p.walkPagesTree(kidNum, box, hasBox, rotate, resources, visited, depth+1); err != nil {
				return err
			}
			if len(p.pages) > MaxPages {
				return fmt.Errorf("%w: PDF exceeds maximum %d pages", ErrInvalidPDF, MaxPages)
			}
		}
		return nil
	}

	if hasNameValue(obj.Dict, "/Type", "/Page") {
		if !hasBox {
			box = [4]float64{0, 0, A4WidthMM * PointsPerMM, A4HeightMM * PointsPerMM}
			hasBox = true
		}
		p.pages = append(p.pages, pdfPageNode{
			ObjNum:    objNum,
			Dict:      obj.Dict,
			MediaBox:  box,
			HasBox:    hasBox,
			Rotate:    rotate,
			Resources: resources,
		})
	}
	return nil
}

func (p *ParsedPDF) extractKids(dict []byte) []int {
	val := extractDictEntryValue(dict, "/Kids")
	if len(val) == 0 {
		return nil
	}
	// Resolve indirect /Kids array if needed.
	if ref, ok := parseSingleRef(val); ok {
		if target, exists := p.objects[ref.Num]; exists {
			val = target.Raw
		}
	}
	matches := indirectRefRE.FindAllSubmatch(val, -1)
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		if num, err := strconv.Atoi(string(m[1])); err == nil && num > 0 {
			out = append(out, num)
		}
	}
	return out
}

func (p *ParsedPDF) extractRect(dict []byte, key string) ([4]float64, bool) {
	val := extractDictEntryValue(dict, key)
	if len(val) == 0 {
		return [4]float64{}, false
	}
	if ref, ok := parseSingleRef(val); ok {
		if target, exists := p.objects[ref.Num]; exists {
			val = target.Raw
		}
	}
	nums := numberTokenRE.FindAllString(string(val), 4)
	if len(nums) != 4 {
		return [4]float64{}, false
	}
	var rect [4]float64
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseFloat(nums[i], 64)
		if err != nil {
			return [4]float64{}, false
		}
		rect[i] = v
	}
	return rect, true
}

// ExtractPageClosure returns the materialized page dictionary (without /Parent,
// with inherited /MediaBox and /Resources present, and /Rotate updated by
// extraRotation) along with the transitive closure of referenced objects.
func (p *ParsedPDF) ExtractPageClosure(pageNum int, extraRotation int) (pageDict []byte, deps map[int][]byte, err error) {
	if pageNum < 1 || pageNum > len(p.pages) {
		return nil, nil, fmt.Errorf("source page %d out of range (1..%d)", pageNum, len(p.pages))
	}
	pg := p.pages[pageNum-1]

	// Build a clean page dictionary: remove /Parent and /Rotate, ensure
	// /MediaBox and /Resources are present, and set the merged /Rotate.
	dict := removeDictKey(pg.Dict, "/Parent")
	dict = removeDictKey(dict, "/Rotate")
	if len(extractDictEntryValue(dict, "/MediaBox")) == 0 && pg.HasBox {
		boxEntry := fmt.Sprintf("/MediaBox [%.3f %.3f %.3f %.3f]", pg.MediaBox[0], pg.MediaBox[1], pg.MediaBox[2], pg.MediaBox[3])
		dict = insertDictEntry(dict, boxEntry)
	}
	if len(extractDictEntryValue(dict, "/Resources")) == 0 && len(pg.Resources) > 0 {
		dict = insertDictEntry(dict, "/Resources "+string(pg.Resources))
	}
	finalRot := ((pg.Rotate+extraRotation)%360 + 360) % 360
	if finalRot != 0 {
		dict = insertDictEntry(dict, fmt.Sprintf("/Rotate %d", finalRot))
	}

	// BFS all indirect references reachable from the page dictionary.
	deps = make(map[int][]byte)
	queue := findAllRefs(dict)
	for len(queue) > 0 {
		refNum := queue[0]
		queue = queue[1:]
		if refNum == pg.ObjNum {
			continue
		}
		if _, seen := deps[refNum]; seen {
			continue
		}
		obj, ok := p.objects[refNum]
		if !ok {
			continue
		}
		// Never pull in /Pages or /Catalog objects accidentally (e.g. via /StructParents or /P).
		if len(obj.Dict) > 0 && (hasNameValue(obj.Dict, "/Type", "/Pages") || hasNameValue(obj.Dict, "/Type", "/Catalog")) {
			continue
		}
		deps[refNum] = obj.Raw
		// Only scan dictionary / non-stream part for indirect references so
		// binary stream bytes are never mistaken for "N 0 R".
		scanTarget := obj.Raw
		if obj.HasStr && len(obj.Dict) > 0 {
			scanTarget = obj.Dict
		}
		for _, child := range findAllRefs(scanTarget) {
			if _, seen := deps[child]; !seen && child != pg.ObjNum {
				queue = append(queue, child)
			}
		}
	}
	return dict, deps, nil
}

// --- Low-level PDF dictionary/token helpers ---

func isPDFWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' || b == 0
}

func isPDFDelimiter(b byte) bool {
	switch b {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func findMatchingDictEnd(data []byte, start int) int {
	if start+1 >= len(data) || data[start] != '<' || data[start+1] != '<' {
		return -1
	}
	depth := 0
	inParen := 0
	for i := start; i < len(data); i++ {
		if inParen > 0 {
			if data[i] == '\\' {
				i++
				continue
			}
			if data[i] == '(' {
				inParen++
			} else if data[i] == ')' {
				inParen--
			}
			continue
		}
		if data[i] == '(' {
			inParen = 1
			continue
		}
		if data[i] == '%' {
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			continue
		}
		if data[i] == '<' && i+1 < len(data) && data[i+1] == '<' {
			depth++
			i++
			continue
		}
		if data[i] == '>' && i+1 < len(data) && data[i+1] == '>' {
			depth--
			i++
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// extractDictEntrySpan returns [keyStart, valStart, valEnd] within a top-level
// dictionary slice `<< ... >>`. Returns ok=false if key is not at top level.
func extractDictEntrySpan(dict []byte, key string) (keyStart, valStart, valEnd int, ok bool) {
	if len(dict) < 4 || dict[0] != '<' || dict[1] != '<' {
		return 0, 0, 0, false
	}
	i := 2
	limit := len(dict) - 2
	for i < limit {
		i = skipWSAndComments(dict, i, limit)
		if i >= limit {
			break
		}
		if dict[i] != '/' {
			// Skip unexpected token.
			i = skipPDFValue(dict, i, limit)
			continue
		}
		kStart := i
		i++
		for i < limit && !isPDFWhitespace(dict[i]) && !isPDFDelimiter(dict[i]) {
			i++
		}
		kName := string(dict[kStart:i])
		vStart := skipWSAndComments(dict, i, limit)
		vEnd := skipPDFValue(dict, vStart, limit)
		if kName == key {
			return kStart, vStart, vEnd, true
		}
		i = vEnd
	}
	return 0, 0, 0, false
}

func extractDictEntryValue(dict []byte, key string) []byte {
	_, vs, ve, ok := extractDictEntrySpan(dict, key)
	if !ok {
		return nil
	}
	return bytes.TrimSpace(dict[vs:ve])
}

func removeDictKey(dict []byte, key string) []byte {
	ks, _, ve, ok := extractDictEntrySpan(dict, key)
	if !ok {
		return dict
	}
	out := make([]byte, 0, len(dict)-(ve-ks))
	out = append(out, dict[:ks]...)
	out = append(out, ' ')
	out = append(out, dict[ve:]...)
	return out
}

func insertDictEntry(dict []byte, entry string) []byte {
	if len(dict) < 4 || dict[len(dict)-2] != '>' || dict[len(dict)-1] != '>' {
		return dict
	}
	var buf bytes.Buffer
	buf.Write(dict[:len(dict)-2])
	buf.WriteByte(' ')
	buf.WriteString(entry)
	buf.WriteString(" >>")
	return buf.Bytes()
}

func skipWSAndComments(data []byte, i, limit int) int {
	for i < limit {
		if isPDFWhitespace(data[i]) {
			i++
			continue
		}
		if data[i] == '%' {
			for i < limit && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			continue
		}
		break
	}
	return i
}

func skipPDFValue(data []byte, i, limit int) int {
	i = skipWSAndComments(data, i, limit)
	if i >= limit {
		return limit
	}
	switch {
	case data[i] == '<' && i+1 < limit && data[i+1] == '<':
		end := findMatchingDictEnd(data[:limit+2], i)
		if end > i && end <= limit {
			return end
		}
		return limit
	case data[i] == '<':
		for i < limit && data[i] != '>' {
			i++
		}
		if i < limit {
			i++
		}
		return i
	case data[i] == '[':
		depth := 0
		inParen := 0
		for i < limit {
			if inParen > 0 {
				if data[i] == '\\' {
					i += 2
					continue
				}
				if data[i] == '(' {
					inParen++
				} else if data[i] == ')' {
					inParen--
				}
				i++
				continue
			}
			if data[i] == '(' {
				inParen = 1
			} else if data[i] == '[' {
				depth++
			} else if data[i] == ']' {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return limit
	case data[i] == '(':
		depth := 0
		for i < limit {
			if data[i] == '\\' {
				i += 2
				continue
			}
			if data[i] == '(' {
				depth++
			} else if data[i] == ')' {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return limit
	default:
		// Scalar token; could be start of an indirect ref "N G R".
		t1End := readScalarEnd(data, i, limit)
		if isAllDigits(data[i:t1End]) {
			j := skipWSAndComments(data, t1End, limit)
			t2End := readScalarEnd(data, j, limit)
			if t2End > j && isAllDigits(data[j:t2End]) {
				k := skipWSAndComments(data, t2End, limit)
				t3End := readScalarEnd(data, k, limit)
				if t3End-k == 1 && data[k] == 'R' {
					return t3End
				}
			}
		}
		return t1End
	}
}

func readScalarEnd(data []byte, i, limit int) int {
	if i < limit && data[i] == '/' {
		i++
	}
	for i < limit && !isPDFWhitespace(data[i]) && !isPDFDelimiter(data[i]) {
		i++
	}
	return i
}

func isAllDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func hasNameValue(dict []byte, key, expected string) bool {
	return string(extractDictEntryValue(dict, key)) == expected
}

func extractDirectInt(dict []byte, key string) (int, bool) {
	val := extractDictEntryValue(dict, key)
	if len(val) == 0 {
		return 0, false
	}
	// Reject indirect refs like "12 0 R".
	if bytes.ContainsRune(val, 'R') {
		return 0, false
	}
	v, err := strconv.Atoi(string(val))
	if err != nil {
		return 0, false
	}
	return v, true
}

func extractRef(dict []byte, key string) (pdfIndirectRef, bool) {
	return parseSingleRef(extractDictEntryValue(dict, key))
}

func parseSingleRef(val []byte) (pdfIndirectRef, bool) {
	fields := strings.Fields(string(val))
	if len(fields) != 3 || fields[2] != "R" {
		return pdfIndirectRef{}, false
	}
	n, err1 := strconv.Atoi(fields[0])
	g, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil || n <= 0 || g < 0 {
		return pdfIndirectRef{}, false
	}
	return pdfIndirectRef{Num: n, Gen: g}, true
}

func findAllRefs(raw []byte) []int {
	matches := indirectRefRE.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(matches))
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(string(m[1]))
		if err != nil || n <= 0 {
			continue
		}
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	return out
}

// renumberPDFRefs rewrites indirect references "N G R" inside a PDF dictionary
// or non-stream object using mapping[oldNum] = newNum, leaving stream payloads
// untouched.
func renumberPDFRefs(raw []byte, mapping map[int]int) []byte {
	// If this is a stream object, only renumber the dictionary before "stream".
	obj := parseRawObject(raw)
	if obj.HasStr && len(obj.Dict) > 0 {
		renumberedDict := replaceRefsInSlice(obj.Dict, mapping)
		var buf bytes.Buffer
		buf.Write(renumberedDict)
		buf.Write(raw[len(obj.Dict):])
		return buf.Bytes()
	}
	return replaceRefsInSlice(raw, mapping)
}

func replaceRefsInSlice(src []byte, mapping map[int]int) []byte {
	return indirectRefRE.ReplaceAllFunc(src, func(match []byte) []byte {
		sub := indirectRefRE.FindSubmatch(match)
		oldNum, err := strconv.Atoi(string(sub[1]))
		if err != nil {
			return match
		}
		if newNum, ok := mapping[oldNum]; ok {
			return []byte(fmt.Sprintf("%d 0 R", newNum))
		}
		return match
	})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
