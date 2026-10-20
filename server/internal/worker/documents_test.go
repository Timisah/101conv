package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
)

func TestPDFPublicationGuard(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		valid      bool
	}{
		{"pdf17", "%PDF-1.7\nfixture\n%%EOF\n", true},
		{"pdf20", "%PDF-2.0\nfixture\n%%EOF\n", true},
		{"text", "not a PDF file\n%%EOF", false},
		{"truncated", "%PDF-1.7\nfixture", false},
		{"badversion", "%PDF-9.9\nfixture\n%%EOF", false},
		{"short", "%PDF", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "out.pdf")
			if err := os.WriteFile(p, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateImageSignature(p, "pdf"); (err == nil) != tt.valid {
				t.Fatalf("valid=%v: %v", tt.valid, err)
			}
		})
	}
}

// Run in Dockerfile.documents-test so production libraries, non-root identity,
// read-only filesystem and no-network conversion isolation are exercised.
func TestDocumentRuntimeConversions(t *testing.T) {
	if os.Getenv("TEST_DOCUMENT_RUNTIME") != "1" {
		t.Skip("run scripts/verify-documents.sh for the pinned document runtime")
	}
	repo := os.Getenv("DOCUMENT_REPOSITORY_ROOT")
	root := t.TempDir()
	fixtures := filepath.Join(root, "fixtures")
	os.MkdirAll(fixtures, 0700)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "HOME="+root, "SAL_USE_VCLPLUGIN=svp")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fixtures, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("input.rtf", `{\rtf1\ansi{\fonttbl{\f0 Missing101convFont;}}\f0 DocumentSentinel42\par Second line}`)
	write("input.txt", "DocumentSentinel42\nSecond line\n")
	write("sheet.csv", "DocumentSentinel42,42\n")
	write("slides.fodp", `<?xml version="1.0"?><office:document xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" office:version="1.3" office:mimetype="application/vnd.oasis.opendocument.presentation"><office:body><office:presentation><draw:page draw:name="Slide1"><draw:frame svg:width="20cm" svg:height="5cm" svg:x="1cm" svg:y="1cm"><draw:text-box><text:p>DocumentSentinel42</text:p></draw:text-box></draw:frame></draw:page></office:presentation></office:body></office:document>`)
	for _, f := range []struct {
		source  string
		targets []string
	}{{"input.rtf", []string{"docx", "doc", "odt"}}, {"sheet.csv", []string{"xlsx", "xls", "ods"}}, {"slides.fodp", []string{"pptx", "ppt", "odp"}}} {
		for _, target := range f.targets {
			run("/usr/bin/libreoffice", "-env:UserInstallation=file://"+root+"/fixture-profile", "--headless", "--convert-to", target, "--outdir", fixtures, filepath.Join(fixtures, f.source))
		}
	}
	engineRoot := os.Getenv("DOCUMENT_ENGINE_ROOT")
	uploadRoot := filepath.Join(root, "uploads")
	os.MkdirAll(uploadRoot, 0700)
	versions := map[string]string{}
	profiles := map[string]profile.Definition{}
	for _, name := range []string{"libreoffice", "poppler"} {
		var m struct {
			Engine engine.Definition `json:"engine"`
		}
		b, err := os.ReadFile(filepath.Join(repo, "catalog/targets/engines", name+"-linux-amd64.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if err = m.Engine.Validate(); err != nil {
			t.Fatal(err)
		}
		versions[name] = m.Engine.Version
		for _, exe := range m.Engine.Executables {
			data, err := os.ReadFile(filepath.Join(engineRoot, name, m.Engine.Version, exe))
			if err != nil {
				t.Fatal(err)
			}
			expected, err := os.ReadFile(filepath.Join(repo, "engines/document-tools", exe))
			if err != nil || string(data) != string(expected) {
				t.Fatal("managed package differs from adapter source")
			}
		}
		var p struct {
			Profile profile.Definition `json:"profile"`
		}
		b, err = os.ReadFile(filepath.Join(repo, "catalog/targets/profiles", name+"-pdf-linux-amd64.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		if err = p.Profile.Validate(); err != nil {
			t.Fatal(err)
		}
		profiles[name] = p.Profile
	}
	runner := &ProcessRunner{EngineRoot: engineRoot, UploadRoot: uploadRoot, ResultRoot: filepath.Join(root, "results"), WorkRoot: filepath.Join("/work", filepath.Base(root)), Timeout: 30 * time.Second}
	var counter atomic.Int64
	convert := func(name, source, target string, options map[string]string) (Result, error) {
		t.Helper()
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		number := counter.Add(1)
		id := fmt.Sprintf("upload-%d", number)
		if err = os.WriteFile(filepath.Join(uploadRoot, id), data, 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		ext := strings.TrimPrefix(filepath.Ext(source), ".")
		var rule profile.ConversionRule
		for _, r := range profiles[name].Rules {
			for _, s := range r.Sources {
				for _, dst := range r.Targets {
					if s == ext && dst == target {
						rule = r
					}
				}
			}
		}
		if len(rule.Arguments) == 0 {
			t.Fatalf("no rule for %s -> %s", ext, target)
		}
		args := append([]string(nil), rule.Arguments...)
		for i := range args {
			for _, o := range rule.Options {
				value := o.Default
				if v, ok := options[o.Key]; ok {
					value = v
				}
				args[i] = strings.ReplaceAll(args[i], "{option:"+o.Key+"}", value)
			}
		}
		return runner.Run(context.Background(), Invocation{JobID: fmt.Sprintf("job-%d", number), Attempt: 1, InputID: id, EngineID: name, EngineVersion: versions[name], ExecutableRelativePath: rule.Executable, Arguments: args, TargetExtension: target, SourceExtension: ext, ExpectedInputSHA256: hex.EncodeToString(digest[:]), ExpectedInputSize: int64(len(data))})
	}
	var pdf string
	for _, ext := range []string{"docx", "doc", "odt", "xlsx", "xls", "ods", "pptx", "ppt", "odp", "rtf", "txt"} {
		t.Run(ext, func(t *testing.T) {
			base := "input"
			if ext == "xlsx" || ext == "xls" || ext == "ods" {
				base = "sheet"
			}
			if ext == "pptx" || ext == "ppt" || ext == "odp" {
				base = "slides"
			}
			result, err := convert("libreoffice", filepath.Join(fixtures, base+"."+ext), "pdf", nil)
			if err != nil {
				t.Fatalf("%v\n%s\n%s", err, result.Stdout, result.Stderr)
			}
			path := filepath.Join(runner.ResultRoot, result.ResultKey)
			text := run("/usr/bin/pdftotext", path, "-")
			if !strings.Contains(text, "DocumentSentinel42") {
				t.Fatalf("PDF lost expected text: %q", text)
			}
			if ext == "docx" {
				data, _ := os.ReadFile(path)
				pdf = filepath.Join(fixtures, "result.pdf")
				os.WriteFile(pdf, data, 0600)
			}
		})
	}
	if pdf == "" {
		t.Fatal("no reference PDF")
	}
	for _, target := range []string{"png", "jpg", "jpeg", "txt"} {
		t.Run("pdf-to-"+target, func(t *testing.T) {
			result, err := convert("poppler", pdf, target, nil)
			if err != nil {
				t.Fatalf("%v: %s", err, result.Stderr)
			}
			if target == "txt" {
				data, _ := os.ReadFile(filepath.Join(runner.ResultRoot, result.ResultKey))
				if !strings.Contains(string(data), "DocumentSentinel42") {
					t.Fatal("text extraction lost sentinel")
				}
			}
		})
	}
	t.Run("missing-page", func(t *testing.T) {
		_, err := convert("poppler", pdf, "png", map[string]string{"page": "9999"})
		if err == nil {
			t.Fatal("missing page succeeded")
		}
	})
	t.Run("corrupt-pdf", func(t *testing.T) {
		write("corrupt.pdf", "%PDF-1.7\nnot a document\n%%EOF")
		if _, err := convert("poppler", filepath.Join(fixtures, "corrupt.pdf"), "txt", nil); err == nil {
			t.Fatal("corrupt PDF succeeded")
		}
	})
	t.Run("corrupt-docx", func(t *testing.T) {
		write("corrupt.docx", "PK\x03\x04garbage")
		if _, err := convert("libreoffice", filepath.Join(fixtures, "corrupt.docx"), "pdf", nil); err == nil {
			t.Fatal("corrupt DOCX succeeded")
		}
	})
	t.Run("pdfa-tagged", func(t *testing.T) {
		result, err := convert("libreoffice", filepath.Join(fixtures, "input.docx"), "pdf", map[string]string{"conformance": "pdfa-2b"})
		if err != nil {
			t.Fatalf("%v: %s", err, result.Stderr)
		}
		info := run("/usr/bin/pdfinfo", filepath.Join(runner.ResultRoot, result.ResultKey))
		if !strings.Contains(info, "Tagged:          yes") {
			t.Fatalf("not tagged: %s", info)
		}
	})

	t.Run("encrypted-pdf", func(t *testing.T) {
		out := filepath.Join(fixtures, "encrypted")
		if err := os.Mkdir(out, 0700); err != nil {
			t.Fatal(err)
		}
		run("/usr/bin/libreoffice", "-env:UserInstallation=file://"+root+"/fixture-profile", "--headless", "--convert-to", `pdf:writer_pdf_Export:{"EncryptFile":{"type":"boolean","value":"true"},"DocumentOpenPassword":{"type":"string","value":"test-password"}}`, "--outdir", out, filepath.Join(fixtures, "input.txt"))
		if _, err := convert("poppler", filepath.Join(out, "input.pdf"), "txt", nil); err == nil {
			t.Fatal("accepted password-protected PDF")
		}
	})
	t.Run("no-text", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("%PDF-1.7\n")
		offsets := []int{0}
		for i, object := range []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> >>"} {
			offsets = append(offsets, b.Len())
			fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
		}
		xref := b.Len()
		fmt.Fprintf(&b, "xref\n0 4\n0000000000 65535 f \n")
		for _, offset := range offsets[1:] {
			fmt.Fprintf(&b, "%010d 00000 n \n", offset)
		}
		fmt.Fprintf(&b, "trailer\n<< /Root 1 0 R /Size 4 >>\nstartxref\n%d\n%%%%EOF\n", xref)
		write("scanned.pdf", b.String())
		result, err := convert("poppler", filepath.Join(fixtures, "scanned.pdf"), "txt", nil)
		if err == nil || !strings.Contains(result.Stderr, "no extractable text") {
			t.Fatalf("blank PDF: %v %s", err, result.Stderr)
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				result, err := convert("libreoffice", filepath.Join(fixtures, "input.docx"), "pdf", nil)
				if err != nil {
					t.Fatalf("concurrent conversion: %v %s", err, result.Stderr)
				}
			})
		}
	})
	t.Run("output-limit", func(t *testing.T) {
		runner.MaxOutput = 1
		defer func() { runner.MaxOutput = 0 }()
		if _, err := convert("libreoffice", filepath.Join(fixtures, "input.docx"), "pdf", nil); err == nil {
			t.Fatal("accepted oversized PDF")
		}
	})
	entries, err := os.ReadDir(runner.WorkRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("attempt workspaces leaked: %v %v", entries, err)
	}
}
