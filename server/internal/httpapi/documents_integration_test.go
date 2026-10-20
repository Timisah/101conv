package httpapi

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/automation"
	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/worker"
)

// Real PDF artifacts travel through the same HTTP, management, database queue,
// Worker, batch archive and mounted-drive publication paths as deployed jobs.
func TestDocumentJobPlatform(t *testing.T) {
	if os.Getenv("TEST_DOCUMENT_RUNTIME") != "1" || integrationDatabaseURL() == "" {
		t.Skip("run scripts/verify-documents.sh for document platform integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", integrationDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("document_platform_%d", time.Now().UnixNano())
	if _, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer adminDB.Exec("DROP SCHEMA " + schema + " CASCADE")
	address, err := url.Parse(integrationDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	q := address.Query()
	q.Set("search_path", schema)
	address.RawQuery = q.Encode()
	db, err := sql.Open("pgx", address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	root, err := os.MkdirTemp("/test-state", "platform-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	manager, err := management.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	repo := os.Getenv("DOCUMENT_REPOSITORY_ROOT")
	var em struct {
		Engine engine.Definition `json:"engine"`
	}
	readJSON := func(p string, dst any) {
		t.Helper()
		data, e := os.ReadFile(filepath.Join(repo, p))
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(data, dst); e != nil {
			t.Fatal(e)
		}
	}
	readJSON("catalog/targets/engines/libreoffice-linux-amd64.json", &em)
	// Verify and extract the actual catalog archive, then install it through the
	// managed installation boundary (never a system-executable override).
	archive, err := os.ReadFile(filepath.Join(repo, "catalog/targets", em.Engine.Artifact.URL))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != em.Engine.Artifact.SHA256 || int64(len(archive)) != em.Engine.Artifact.SizeBytes {
		t.Fatal("adapter archive integrity mismatch")
	}
	stage, cleanup, err := manager.CreateVerifiedStaging()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Typeflag != tar.TypeReg || strings.ContainsAny(h.Name, "/\\") || h.Name == ".." {
			t.Fatal("unsafe test archive")
		}
		data, e := io.ReadAll(io.LimitReader(tr, 1<<20))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(stage, h.Name), data, os.FileMode(h.Mode)); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = manager.InstallVerifiedEngine(em.Engine, stage, em.Engine.Artifact.SHA256, "document-test"); err != nil {
		t.Fatal(err)
	}
	var pm struct {
		Profile profile.Definition `json:"profile"`
	}
	readJSON("catalog/targets/profiles/libreoffice-pdf-linux-amd64.json", &pm)
	if _, err = manager.InstallProfile(ctx, pm.Profile, "document-test"); err != nil {
		t.Fatal(err)
	}
	svc, err := jobs.NewService(db, manager, filepath.Join(root, "uploads"), filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = accounts.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := accounts.CreateInitialAdmin(ctx, "document-admin", "document test administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if err = accounts.CompleteInitialSetup(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	auto, err := automation.NewService(db, []string{root}, svc, manager, accounts)
	if err != nil {
		t.Fatal(err)
	}
	if err = auto.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	runner := &worker.ProcessRunner{EngineRoot: filepath.Join(manager.RootPath(), "engines"), UploadRoot: svc.UploadRoot(), ResultRoot: svc.ResultRoot(), WorkRoot: "/work/platform", Timeout: 30 * time.Second}
	queue, err := jobs.NewWorker(db, runner, jobs.WorkerConfig{ID: "document-platform", PollInterval: 20 * time.Millisecond, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- queue.Run(workerCtx) }()
	defer func() { stopWorker(); <-done }()
	cat, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithAutomation("0.1.0-dev", cat, manager, nil, svc, accounts, auto, "document-admin-token"))
	defer server.Close()

	user, err := accounts.CreateUser(ctx, auth.NewUser{Username: "document-user", Password: "document user test password", RoleID: "converter", Quotas: &auth.Quotas{MaxFileBytes: auth.QuotaLimit{Mode: "unlimited"}, MonthlyJobs: auth.QuotaLimit{Mode: "unlimited"}, MonthlyBytes: auth.QuotaLimit{Mode: "unlimited"}}})
	if err != nil {
		t.Fatal(err)
	}
	key, err := accounts.CreateAPIKey(ctx, user.ID, "document conversion", []string{auth.ScopeRead, auth.ScopeUpload, auth.ScopeSubmit, auth.ScopeResult})
	if err != nil {
		t.Fatal(err)
	}
	authenticated := &http.Client{}
	for _, fixture := range []struct{ name, content, target string }{{"report.rtf", `{\rtf1\ansi DocumentPlatformSentinel}`, "docx"}, {"sheet.csv", "DocumentPlatformSentinel,42\n", "xlsx"}} {
		if err = os.WriteFile(filepath.Join(root, fixture.name), []byte(fixture.content), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, "/usr/bin/libreoffice", "-env:UserInstallation=file://"+root+"/fixtures-profile", "--headless", "--convert-to", fixture.target, "--outdir", root, filepath.Join(root, fixture.name))
		cmd.Env = append(os.Environ(), "HOME="+root, "SAL_USE_VCLPLUGIN=svp")
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("generate %s: %v %s", fixture.target, e, out)
		}
	}
	word, err := os.ReadFile(filepath.Join(root, "report.docx"))
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := os.ReadFile(filepath.Join(root, "sheet.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	call := func(client *http.Client, method, path, contentType string, body io.Reader, want int) []byte {
		t.Helper()
		req, e := http.NewRequestWithContext(ctx, method, server.URL+"/api/v1"+path, body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Origin", server.URL)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if client == authenticated {
			req.Header.Set("Authorization", "Bearer "+key.Token)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s %s: HTTP %d expected %d: %s", method, path, resp.StatusCode, want, data)
		}
		if strings.HasSuffix(path, "/result") && resp.Header.Get("Content-Type") != "application/pdf" {
			t.Fatalf("wrong PDF MIME type: %s", resp.Header.Get("Content-Type"))
		}
		return data
	}
	jar, _ := cookiejar.New(nil)
	guest := &http.Client{Jar: jar}
	call(guest, "POST", "/auth/guest-session", "application/json", strings.NewReader("{}"), http.StatusCreated)
	upload := func(client *http.Client, name, content string) string {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, e := mw.CreateFormFile("file", name)
		if e != nil {
			t.Fatal(e)
		}
		io.WriteString(part, content)
		mw.Close()
		data := call(client, "POST", "/uploads", mw.FormDataContentType(), &body, http.StatusCreated)
		var response jobs.Upload
		if e = json.Unmarshal(data, &response); e != nil || response.ID == "" {
			t.Fatalf("upload: %s %v", data, e)
		}
		return response.ID
	}
	for _, visitor := range []struct {
		name   string
		client *http.Client
	}{{"guest", guest}, {"authenticated", authenticated}} {
		t.Run(visitor.name, func(t *testing.T) {
			id := upload(visitor.client, "report.docx", string(word))
			second := upload(visitor.client, "sheet.xlsx", string(sheet))
			req := jobs.BatchRequest{Mode: "convert", IdempotencyKey: visitor.name + "-document-batch-001", Items: []jobs.BatchItemRequest{{UploadID: id, TargetFormat: "pdf"}, {UploadID: second, TargetFormat: "pdf"}}}
			body, _ := json.Marshal(req)
			data := call(visitor.client, "POST", "/batches", "application/json", bytes.NewReader(body), http.StatusAccepted)
			var response struct {
				Batch jobs.Batch `json:"batch"`
			}
			if e := json.Unmarshal(data, &response); e != nil || response.Batch.ID == "" {
				t.Fatalf("batch: %s %v", data, e)
			}
			for {
				data = call(visitor.client, "GET", "/batches/"+response.Batch.ID, "", nil, http.StatusOK)
				json.Unmarshal(data, &response.Batch)
				if response.Batch.Failed > 0 {
					t.Fatalf("batch failed: %s", data)
				}
				if response.Batch.Succeeded == 2 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("batch timed out")
				case <-time.After(30 * time.Millisecond):
				}
			}
			for _, item := range response.Batch.Items {
				if item.Job == nil || item.Job.ProfileID != pm.Profile.ID {
					t.Fatalf("automatic document profile not selected: %+v", item)
				}
				result := call(visitor.client, "GET", "/jobs/"+item.JobID+"/result", "", nil, http.StatusOK)
				if !bytes.HasPrefix(result, []byte("%PDF-")) {
					t.Fatal("invalid PDF download")
				}
				if visitor.name == "authenticated" {
					call(guest, "GET", "/jobs/"+item.JobID, "", nil, http.StatusNotFound)
				}
			}
			data = call(visitor.client, "GET", "/batches/"+response.Batch.ID+"/results.zip", "", nil, http.StatusOK)
			z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if e != nil || len(z.File) != 2 {
				t.Fatalf("batch archive: %v", e)
			}
		})
	}
	if t.Failed() {
		t.FailNow()
	}
	input := filepath.Join(root, "input")
	output := filepath.Join(root, "output")
	os.Mkdir(input, 0700)
	os.Mkdir(output, 0700)
	in, err := auto.CreateDrive(ctx, "Documents", input, "read")
	if err != nil {
		t.Fatal(err)
	}
	out, err := auto.CreateDrive(ctx, "PDFs", output, "read_write")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(input, "watched.rtf"), []byte(`{\rtf1\ansi FolderSentinel}`), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := auto.CreateAutomation(ctx, admin.ID, "Office folder to PDF", automation.Definition{Trigger: automation.Trigger{Type: "folder.scan", InputDriveID: in.ID, IntervalSeconds: 30, StabilitySeconds: 5, MaxConcurrent: 1, ProcessExistingOnNext: true}, Filter: automation.FileFilter{Extensions: []string{"rtf"}}, Action: automation.FileAction{Mode: "convert", ProfileID: pm.Profile.ID, TargetFormat: "pdf", OutputDriveID: out.ID, OutputBehavior: "unique"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auto.SetEnabled(ctx, a.ID, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = auto.RunNow(ctx, a.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	autoCtx, stopAuto := context.WithCancel(ctx)
	autoDone := make(chan struct{})
	go func() { defer close(autoDone); auto.Run(autoCtx) }()
	defer func() { stopAuto(); <-autoDone }()
	for {
		files, e := os.ReadDir(output)
		if e != nil {
			t.Fatal(e)
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".pdf") {
				data, e := os.ReadFile(filepath.Join(output, f.Name()))
				if e != nil || !bytes.HasPrefix(data, []byte("%PDF-")) {
					t.Fatalf("automation output: %v", e)
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			history, _ := auto.History(context.Background(), a.ID, admin.ID, 50)
			t.Fatalf("automation timed out: %+v", history)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
