package httpapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/timisah/101conv/server/internal/automation"
	"github.com/timisah/101conv/server/internal/immich"
	"github.com/timisah/101conv/server/internal/limits"
)

func (a *API) listExtensionCatalog(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if !a.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"extensions": automation.BuiltinExtensionCatalog(),
		"apiVersion": automation.ExtensionAPIVersion,
	})
}

func (a *API) immichReady(w http.ResponseWriter) bool {
	if a.immich == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "immich_unavailable", "Immich connector store is unavailable")
		return false
	}
	return true
}

func (a *API) getImmichConfig(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.requireAdmin(w, r) || !a.immichReady(w) {
		return
	}
	view, err := a.immich.GetView(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "immich_config_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connector": view})
}

func (a *API) saveImmichConfig(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.requireAdmin(w, r) || !a.immichReady(w) {
		return
	}
	var req struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	view, err := a.immich.Save(r.Context(), req.BaseURL, req.APIKey)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "immich_config_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connector": view})
}

func (a *API) testImmichConnection(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.requireAdmin(w, r) || !a.immichReady(w) {
		return
	}
	client, err := a.immich.LoadClient(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "immich_not_configured", err.Error())
		return
	}
	about, err := client.About(r.Context())
	if err != nil {
		_ = a.immich.RecordCheck(r.Context(), "", false, err.Error())
		paused := 0
		if a.automations != nil {
			paused, _ = a.automations.PauseAutomationsForExtension(r.Context(), "immich-connector", err.Error())
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                false,
			"compatible":        false,
			"error":             err.Error(),
			"pausedAutomations": paused,
			"disclosure":        immich.HonestDisclosure,
		})
		return
	}

	constraint := ">=1.115.0 <3.0.0"
	if a.automations != nil {
		if activeExt, found, extErr := a.automations.ActiveExtensionByID(r.Context(), "immich-connector"); extErr == nil && found && activeExt.Manifest.ExternalVersion != "" {
			constraint = activeExt.Manifest.ExternalVersion
		}
	}
	compatible, reason := automation.ExternalVersionCompatible(constraint, about.Version)
	paused := 0
	if !compatible && a.automations != nil {
		paused, _ = a.automations.PauseAutomationsForExtension(r.Context(), "immich-connector", reason)
	}
	_ = a.immich.RecordCheck(r.Context(), about.Version, compatible, reason)
	view, _ := a.immich.GetView(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"version":           about.Version,
		"compatible":        compatible,
		"reason":            reason,
		"pausedAutomations": paused,
		"connector":         view,
		"disclosure":        immich.HonestDisclosure,
	})
}

func (a *API) listImmichAssets(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.requireAdmin(w, r) || !a.immichReady(w) {
		return
	}
	client, err := a.immich.LoadClient(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "immich_not_configured", err.Error())
		return
	}
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, parseErr := strconv.Atoi(raw); parseErr == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	assets, err := client.ListAssets(r.Context(), limit)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "immich_query_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":     assets,
		"disclosure": immich.HonestDisclosure,
	})
}

func (a *API) importImmichAsset(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.requireAdmin(w, r) || !a.immichReady(w) {
		return
	}
	if a.jobs == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job service is unavailable")
		return
	}
	var req struct {
		AssetID  string `json:"assetId"`
		FileName string `json:"fileName,omitempty"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	client, err := a.immich.LoadClient(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "immich_not_configured", err.Error())
		return
	}
	data, detectedName, err := client.DownloadOriginal(r.Context(), strings.TrimSpace(req.AssetID), limits.MaxInputBytes)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "immich_download_failed", err.Error())
		return
	}
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" {
		fileName = detectedName
	}
	p, _ := a.identity(r)
	upload, err := a.jobs.CreateUploadFor(r.Context(), p.ID, limits.MaxInputBytes, nil, fileName, bytes.NewReader(data))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "upload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload":     upload,
		"disclosure": immich.HonestDisclosure,
	})
}

func (a *API) exportJobToImmich(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.requireAdmin(w, r) || !a.immichReady(w) {
		return
	}
	if a.jobs == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job service is unavailable")
		return
	}
	var req struct {
		JobID string `json:"jobId"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	client, err := a.immich.LoadClient(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "immich_not_configured", err.Error())
		return
	}
	p, _ := a.identity(r)
	file, job, downloadName, err := a.jobs.OpenResultFor(r.Context(), strings.TrimSpace(req.JobID), p.ID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "result_unavailable", err.Error())
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limits.MaxOutputBytes+1))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "result_read_failed", err.Error())
		return
	}
	if int64(len(content)) > limits.MaxOutputBytes {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "result_too_large", "result exceeds maximum upload size")
		return
	}
	if downloadName == "" {
		downloadName = fmt.Sprintf("%s.%s", job.ID, job.TargetFormat)
	}
	res, err := client.UploadAsset(r.Context(), downloadName, content, "101conv-job-"+job.ID, time.Now().UTC())
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "immich_upload_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"asset":      res,
		"disclosure": immich.HonestDisclosure,
	})
}
