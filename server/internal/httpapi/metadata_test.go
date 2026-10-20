package httpapi

import (
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/jobs"
)

func TestPrepareMetadataRemoval(t *testing.T) {
	upload := jobs.Upload{ID: "upload-1", Extension: "jpg"}
	request, err := prepareMetadataRemoval(upload, metadataRemovalRequest{ProfileID: "image-profile", IdempotencyKey: "metadata-strip-1"})
	if err != nil {
		t.Fatalf("prepare metadata removal: %v", err)
	}
	if request.TargetFormat != "jpg" || request.Mode != "convert" || request.Options["metadata"] != "-1" {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestPrepareMetadataRemovalRejectsFormatChange(t *testing.T) {
	_, err := prepareMetadataRemoval(jobs.Upload{ID: "upload-1", Extension: "png"}, metadataRemovalRequest{TargetFormat: "webp", IdempotencyKey: "metadata-strip-2"})
	if err == nil || !strings.Contains(err.Error(), "keep the uploaded format") {
		t.Fatalf("expected same-format validation error, got %v", err)
	}
}
