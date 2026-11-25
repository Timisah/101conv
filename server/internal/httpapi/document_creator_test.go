package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/document"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/worker"
)

func TestPostgresDocumentCreatorEndToEnd(t *testing.T) {
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL Document Creator integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accounts, err := auth.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	manager, err := management.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	uploadRoot := filepath.Join(dataDir, "uploads")
	resultRoot := filepath.Join(dataDir, "results")
	workRoot := filepath.Join(dataDir, "work")
	jobService, err := jobs.NewService(db, manager, uploadRoot, resultRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := jobService.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}

	runner := &worker.ProcessRunner{
		EngineRoot: filepath.Join(dataDir, "engines"),
		UploadRoot: uploadRoot,
		ResultRoot: resultRoot,
		WorkRoot:   workRoot,
		Timeout:    30 * time.Second,
	}
	w, err := jobs.NewWorker(db, runner, jobs.WorkerConfig{ID: "doc-test-worker", PollInterval: 20 * time.Millisecond, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = w.Run(ctx)
	}()
	defer func() {
		cancel()
		<-workerDone
	}()

	ts := httptest.NewServer(NewWithAccounts("0.1.0-dev", catalogService, manager, nil, jobService, accounts, "doc-admin-token"))
	defer ts.Close()

	suffix, err := randomTestID()
	if err != nil {
		t.Fatal(err)
	}
	unlimited := &auth.Quotas{
		MaxFileBytes: auth.QuotaLimit{Mode: "unlimited"},
		MonthlyJobs:  auth.QuotaLimit{Mode: "unlimited"},
		MonthlyBytes: auth.QuotaLimit{Mode: "unlimited"},
	}
	userA, err := accounts.CreateUser(ctx, auth.NewUser{Username: "doc-a-" + suffix[:10], Password: "strong-password-1", RoleID: "converter", Quotas: unlimited})
	if err != nil {
		t.Fatal(err)
	}
	userB, err := accounts.CreateUser(ctx, auth.NewUser{Username: "doc-b-" + suffix[:10], Password: "strong-password-2", RoleID: "converter", Quotas: unlimited})
	if err != nil {
		t.Fatal(err)
	}
	keyA, err := accounts.CreateAPIKey(ctx, userA.ID, "doc-a-key", []string{auth.ScopeRead, auth.ScopeUpload, auth.ScopeSubmit, auth.ScopeResult})
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := accounts.CreateAPIKey(ctx, userB.ID, "doc-b-key", []string{auth.ScopeRead, auth.ScopeUpload, auth.ScopeSubmit, auth.ScopeResult})
	if err != nil {
		t.Fatal(err)
	}

	doJSON := func(method, path, apiKey string, payload any) *http.Response {
		t.Helper()
		var body io.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, ts.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	uploadFile := func(apiKey, fileName string, content []byte) string {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
		_ = mw.Close()
		req, err := http.NewRequest("POST", ts.URL+"/api/v1/uploads", &buf)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("upload %s failed (%d): %s", fileName, resp.StatusCode, string(b))
		}
		var out struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.ID
	}

	// 1. Formats endpoint returns all 8 formats.
	resp := doJSON("GET", "/api/v1/documents/formats", keyA.Token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("formats status=%d", resp.StatusCode)
	}
	var fmtResp struct {
		Formats []document.FormatInfo `json:"formats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fmtResp); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(fmtResp.Formats) < 8 {
		t.Fatalf("expected at least 8 document formats, got %d", len(fmtResp.Formats))
	}

	// 2. Upload multi-page vector PDF + JPEG + PNG.
	pdfBytes := buildTestVectorPDF([]string{"DOC_VECTOR_ALPHA", "DOC_VECTOR_BETA"})
	jpgBytes := buildTestJPEG(t, 200, 120)
	pngBytes := buildTestPNG(t, 120, 160)
	pdfID := uploadFile(keyA.Token, "report.pdf", pdfBytes)
	jpgID := uploadFile(keyA.Token, "photo.jpg", jpgBytes)
	pngID := uploadFile(keyA.Token, "diagram.png", pngBytes)

	// 3. Inspect PDF source -> expands to 2 pages.
	resp = doJSON("GET", "/api/v1/documents/sources/"+pdfID, keyA.Token, nil)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("inspect source failed (%d): %s", resp.StatusCode, string(b))
	}
	var srcInfo document.SourceInfo
	if err := json.NewDecoder(resp.Body).Decode(&srcInfo); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if srcInfo.Kind != "pdf" || srcInfo.PageCount != 2 {
		t.Fatalf("expected 2-page PDF source info, got %+v", srcInfo)
	}

	// 4. Create, list, update, enforce cross-user isolation, and delete a project.
	manifest := document.Manifest{
		SchemaVersion: document.SchemaVersion,
		Name:          "Q4 Packet",
		OutputFormat:  "pdf",
		Pages: []document.Page{
			{ID: "p1", UploadID: pdfID, SourcePage: 2, Rotation: 90, SourceName: "report.pdf", SourceKind: "pdf", SourcePageCount: 2},
			{ID: "p2", UploadID: jpgID, SourcePage: 1, Rotation: 0, SourceName: "photo.jpg", SourceKind: "image", SourcePageCount: 1},
			{ID: "p3", UploadID: pdfID, SourcePage: 1, Rotation: 0, SourceName: "report.pdf", SourceKind: "pdf", SourcePageCount: 2},
			{ID: "p4", UploadID: pngID, SourcePage: 1, Rotation: 180, SourceName: "diagram.png", SourceKind: "image", SourcePageCount: 1},
		},
	}
	resp = doJSON("POST", "/api/v1/documents/projects", keyA.Token, manifest)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create project failed (%d): %s", resp.StatusCode, string(b))
	}
	var createOut struct {
		Project document.Project `json:"project"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createOut); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if createOut.Project.ID == "" || createOut.Project.ExpiresAt != nil {
		t.Fatalf("expected non-expiring saved user project, got %+v", createOut.Project)
	}

	// Cross-user isolation: userB cannot read or update userA's project.
	resp = doJSON("GET", "/api/v1/documents/projects/"+createOut.Project.ID, keyB.Token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-user project read, got %d", resp.StatusCode)
	}

	// 5. Export mixed native PDF + JPEG + PNG through the Worker and verify output!
	resp = doJSON("POST", "/api/v1/documents/export", keyA.Token, map[string]any{
		"manifest":       manifest,
		"idempotencyKey": "doc-export-pdf-" + suffix[:12],
	})
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("export PDF failed (%d): %s", resp.StatusCode, string(b))
	}
	var exportOut struct {
		Job jobs.Job `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&exportOut); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Wait for Worker to finish the build job.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r := doJSON("GET", "/api/v1/jobs/"+exportOut.Job.ID, keyA.Token, nil)
		var detail jobs.JobDetail
		_ = json.NewDecoder(r.Body).Decode(&detail)
		r.Body.Close()
		if detail.Job.State == jobs.Succeeded {
			break
		}
		if detail.Job.State == jobs.Failed {
			t.Fatalf("document build job failed: %s", detail.Job.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Download result and verify vector text + verbatim JPEG bytes!
	resp = doJSON("GET", "/api/v1/jobs/"+exportOut.Job.ID+"/result", keyA.Token, nil)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("download build PDF failed (%d): %s", resp.StatusCode, string(b))
	}
	assembledPDF, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(assembledPDF, []byte("DOC_VECTOR_BETA")) || !bytes.Contains(assembledPDF, []byte("DOC_VECTOR_ALPHA")) {
		t.Fatal("expected native PDF vector text to survive Worker assembly")
	}
	if !bytes.Contains(assembledPDF, jpgBytes) {
		t.Fatal("expected original JPEG bytes to survive Worker assembly verbatim")
	}
	parsed, err := document.ParsePDF(assembledPDF)
	if err != nil || parsed.PageCount() != 4 {
		t.Fatalf("expected valid 4-page assembled PDF, err=%v count=%d", err, parsed.PageCount())
	}

	// Verify it appears in /api/v1/history?mode=build!
	resp = doJSON("GET", "/api/v1/history?mode=build", keyA.Token, nil)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("history mode=build failed (%d): %s", resp.StatusCode, string(b))
	}
	var histOut struct {
		Total int `json:"total"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&histOut)
	resp.Body.Close()
	if histOut.Total < 1 {
		t.Fatalf("expected build job in /api/v1/history?mode=build, got total=%d", histOut.Total)
	}

	// 6. Export CBZ from images via Worker and verify download!
	cbzManifest := document.Manifest{
		SchemaVersion: document.SchemaVersion,
		Name:          "Image Pack",
		OutputFormat:  "cbz",
		Pages: []document.Page{
			{ID: "c1", UploadID: jpgID, SourcePage: 1, Rotation: 0},
			{ID: "c2", UploadID: pngID, SourcePage: 1, Rotation: 90},
		},
	}
	resp = doJSON("POST", "/api/v1/documents/export", keyA.Token, map[string]any{
		"manifest":       cbzManifest,
		"idempotencyKey": "doc-export-cbz-" + suffix[:12],
	})
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("export CBZ failed (%d): %s", resp.StatusCode, string(b))
	}
	var cbzOut struct {
		Job jobs.Job `json:"job"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&cbzOut)
	resp.Body.Close()

	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r := doJSON("GET", "/api/v1/jobs/"+cbzOut.Job.ID, keyA.Token, nil)
		var detail jobs.JobDetail
		_ = json.NewDecoder(r.Body).Decode(&detail)
		r.Body.Close()
		if detail.Job.State == jobs.Succeeded {
			break
		}
		if detail.Job.State == jobs.Failed {
			t.Fatalf("CBZ build job failed: %s", detail.Job.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp = doJSON("GET", "/api/v1/jobs/"+cbzOut.Job.ID+"/result", keyA.Token, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), ".cbz") {
		t.Fatalf("unexpected CBZ download response: status=%d disposition=%q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	resp.Body.Close()

	// 7. Delete project -> non-destructive to uploads.
	resp = doJSON("DELETE", "/api/v1/documents/projects/"+createOut.Project.ID, keyA.Token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete project failed: %d", resp.StatusCode)
	}
	resp = doJSON("GET", "/api/v1/documents/sources/"+pdfID, keyA.Token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deleting project must not delete source upload, got %d", resp.StatusCode)
	}
}

func buildTestVectorPDF(texts []string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, 4+len(texts)*2)
	offsets = append(offsets, 0)
	writeObj := func(id int, body string) {
		for len(offsets) <= id {
			offsets = append(offsets, 0)
		}
		offsets[id] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}
	fontID := 3
	pageIDs := make([]int, len(texts))
	nextID := 4
	for i := range texts {
		pageIDs[i] = nextID + 1
		nextID += 2
	}
	var kids strings.Builder
	for i, id := range pageIDs {
		if i > 0 {
			kids.WriteByte(' ')
		}
		fmt.Fprintf(&kids, "%d 0 R", id)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pageIDs)))
	writeObj(fontID, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, text := range texts {
		cid := pageIDs[i] - 1
		stream := fmt.Sprintf("BT /F1 14 Tf 36 72 Td (%s) Tj ET\n", text)
		writeObj(cid, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
		writeObj(pageIDs[i], fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>", fontID, cid))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for i := 1; i < len(offsets); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return buf.Bytes()
}

func buildTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 180, G: 90, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 130, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
