package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/thumbnails"
)

func (a *API) getUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeResult)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	_, path, err := a.jobs.GetUploadFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "upload file is unavailable")
		return
	}
	defer f.Close()
	writeThumbnailResponse(w, f)
}

func (a *API) getJobThumbnail(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeResult)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "result"
	}
	switch kind {
	case "result":
		f, _, _, err := a.jobs.OpenResultFor(r.Context(), r.PathValue("id"), ownerID)
		if err != nil {
			writeJobError(w, err)
			return
		}
		defer f.Close()
		writeThumbnailResponse(w, f)
	case "original":
		f, err := a.jobs.OpenOriginalFor(r.Context(), r.PathValue("id"), ownerID)
		if err != nil {
			writeJobError(w, err)
			return
		}
		defer f.Close()
		writeThumbnailResponse(w, f)
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "kind must be result or original")
	}
}

func writeThumbnailResponse(w http.ResponseWriter, f *os.File) {
	data, contentType, err := thumbnails.Render(f, thumbnails.DefaultMaxEdge)
	if err != nil {
		if errors.Is(err, thumbnails.ErrUnsupportedFormat) || errors.Is(err, thumbnails.ErrImageTooLarge) {
			writeAPIError(w, http.StatusUnsupportedMediaType, "thumbnail_unavailable", err.Error())
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "thumbnail_error", "could not generate thumbnail")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
