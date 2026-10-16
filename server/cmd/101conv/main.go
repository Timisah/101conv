package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/httpapi"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/repositorymanager"
	"github.com/timisah/101conv/server/internal/worker"
)

var buildVersion = "0.1.0-dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "worker":
			if err := runWorker(); err != nil {
				log.Fatal(err)
			}
			return
		case "healthcheck":
			if err := httpHealth(); err != nil {
				log.Fatal(err)
			}
			return
		case "worker-health":
			if err := databaseHealth(); err != nil {
				log.Fatal(err)
			}
			return
		}
	}
	version := env("APP_VERSION", buildVersion)
	port := env("PORT", "5656")
	catalogService, err := catalog.New(version)
	if err != nil {
		log.Fatalf("load built-in catalog: %v", err)
	}
	dataPath := env("DATA_PATH", "./data")
	allowedImportRoots := []string{}
	if configured := os.Getenv("ENGINE_ALLOWED_PATHS"); configured != "" {
		for _, path := range filepath.SplitList(configured) {
			if strings.TrimSpace(path) != "" {
				allowedImportRoots = append(allowedImportRoots, strings.TrimSpace(path))
			}
		}
	} else {
		allowedImportRoots = []string{filepath.Join(dataPath, "imports")}
	}
	managementService, err := management.OpenWithPolicy(dataPath, allowedImportRoots)
	if err != nil {
		log.Fatalf("open management storage: %v", err)
	}
	repositoryService, err := repositorymanager.Open(dataPath, version, managementService)
	if err != nil {
		_ = managementService.Close()
		log.Fatalf("open repository manager: %v", err)
	}
	var db *sql.DB
	var jobService *jobs.Service
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		db, err = openDatabase(dsn)
		if err != nil {
			_ = repositoryService.Close()
			_ = managementService.Close()
			log.Fatalf("open job database: %v", err)
		}
		jobService, err = jobs.NewService(db, managementService, env("UPLOADS_PATH", filepath.Join(dataPath, "uploads")), env("RESULTS_PATH", filepath.Join(dataPath, "results")))
		if err == nil {
			err = jobService.Migrate(context.Background())
		}
		if err != nil {
			_ = db.Close()
			_ = repositoryService.Close()
			_ = managementService.Close()
			log.Fatalf("initialize job service: %v", err)
		}
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           httpapi.NewWithJobServices(version, catalogService, managementService, repositoryService, jobService, os.Getenv("ENGINE_ADMIN_TOKEN")),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { log.Printf("101conv %s listening on :%s", version, port); serveErr <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	case err = <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server: %v", err)
		}
	}
	if db != nil {
		_ = db.Close()
	}
	_ = repositoryService.Close()
	_ = managementService.Close()
}

func openDatabase(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func runWorker() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required for worker role")
	}
	db, err := openDatabase(dsn)
	if err != nil {
		return fmt.Errorf("connect worker to PostgreSQL: %w", err)
	}
	defer db.Close()
	maxOutput := int64Env("MAX_OUTPUT_BYTES", jobs.MaxOutputBytes)
	if maxOutput > jobs.MaxOutputBytes {
		return fmt.Errorf("MAX_OUTPUT_BYTES cannot exceed %d", jobs.MaxOutputBytes)
	}
	runner := &worker.ProcessRunner{EngineRoot: env("ENGINE_ROOT", "/var/lib/101conv/engines"), UploadRoot: env("UPLOADS_PATH", "/var/lib/101conv/uploads"), ResultRoot: env("RESULTS_PATH", "/var/lib/101conv/results"), WorkRoot: env("WORK_PATH", "/work"), Timeout: durationEnv("CONVERSION_TIMEOUT", 10*time.Minute), MaxOutput: maxOutput}
	workerID := env("WORKER_ID", "")
	lease := durationEnv("JOB_LEASE", 45*time.Second)
	runnerService, err := jobs.NewWorker(db, runner, jobs.WorkerConfig{ID: workerID, Lease: lease, PollInterval: time.Second})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("101conv worker started with lease %s", lease)
	err = runnerService.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func httpHealth() error {
	port := env("PORT", "5656")
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	return nil
}
func databaseHealth() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	db, err := openDatabase(dsn)
	if err != nil {
		return err
	}
	return db.Close()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		log.Fatalf("invalid %s", key)
	}
	return d
}
func int64Env(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		log.Fatalf("invalid %s", key)
	}
	return n
}
