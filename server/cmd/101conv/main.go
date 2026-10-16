package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/httpapi"
	"github.com/timisah/101conv/server/internal/management"
)

var buildVersion = "0.1.0-dev"

func main() {
	version := env("APP_VERSION", buildVersion)
	port := env("PORT", "5656")
	catalogService, err := catalog.New(version)
	if err != nil {
		log.Fatalf("load built-in catalog: %v", err)
	}
	managementService, err := management.Open(env("DATA_PATH", "./data"))
	if err != nil {
		log.Fatalf("open management storage: %v", err)
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           httpapi.NewWithManagement(version, catalogService, managementService, os.Getenv("ENGINE_ADMIN_TOKEN")),
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
