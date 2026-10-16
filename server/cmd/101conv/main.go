package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/httpapi"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/repositorymanager"
)

var buildVersion = "0.1.0-dev"

func main() {
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

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           httpapi.NewWithServices(version, catalogService, managementService, repositoryService, os.Getenv("ENGINE_ADMIN_TOKEN")),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("101conv %s listening on :%s", version, port)
	log.Fatal(server.ListenAndServe())
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
