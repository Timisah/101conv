package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/automation"
)

func automationOwner(p auth.Principal) string {
	if p.IsAdmin() {
		return ""
	}
	return p.ID
}

func (a *API) automationService(w http.ResponseWriter) bool {
	if a.automations == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "automations_unavailable", "automation storage is not configured; set AUTOMATION_MOUNT_ROOTS and restart Core")
		return false
	}
	return true
}

func (a *API) listDrives(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	v, err := a.automations.ListDrives(r.Context())
	if err != nil {
		writeAPIError(w, 500, "drive_storage_error", "could not list mounted drives")
		return
	}
	writeJSON(w, 200, map[string]any{"drives": v})
}

type driveBody struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Access  string `json:"access"`
	Enabled bool   `json:"enabled"`
}

func (a *API) createDrive(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	var req driveBody
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.automations.CreateDrive(r.Context(), req.Name, req.Path, req.Access)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"drive": v})
}
func (a *API) updateDrive(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	var req driveBody
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.automations.UpdateDrive(r.Context(), r.PathValue("id"), req.Name, req.Path, req.Access, req.Enabled)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"drive": v})
}
func (a *API) deleteDrive(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	if err := a.automations.RemoveDrive(r.Context(), r.PathValue("id")); err != nil {
		writeAutomationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) testDrive(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	var req struct {
		Path  string `json:"path"`
		Write bool   `json:"write"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := a.automations.TestPath(req.Path, req.Write); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "drive_test_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"accessible": true, "writable": req.Write})
}

func (a *API) listAutomationBlocks(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	writeJSON(w, 200, map[string]any{"blocks": automation.Blocks, "extensionApiVersion": automation.ExtensionAPIVersion})
}
func (a *API) listBlueprints(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	writeJSON(w, 200, map[string]any{"blueprints": automation.BuiltinBlueprints()})
}
func (a *API) listAutomations(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	v, err := a.automations.ListAutomations(r.Context(), automationOwner(p))
	if err != nil {
		writeAPIError(w, 500, "automation_storage_error", "could not list automations")
		return
	}
	writeJSON(w, 200, map[string]any{"automations": v})
}

type automationBody struct {
	Name       string                `json:"name"`
	Definition automation.Definition `json:"definition"`
}

func (a *API) createAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	var req automationBody
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.automations.CreateAutomation(r.Context(), p.ID, req.Name, req.Definition)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"automation": v})
}
func (a *API) getAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	v, err := a.automations.GetAutomation(r.Context(), r.PathValue("id"), automationOwner(p))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"automation": v})
}
func (a *API) updateAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	var req automationBody
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.automations.UpdateAutomation(r.Context(), r.PathValue("id"), automationOwner(p), req.Name, req.Definition)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"automation": v})
}
func (a *API) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	if err := a.automations.DeleteAutomation(r.Context(), r.PathValue("id"), automationOwner(p)); err != nil {
		writeAutomationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) setAutomationEnabled(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.automations.SetEnabled(r.Context(), r.PathValue("id"), automationOwner(p), req.Enabled)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"automation": v})
}
func (a *API) dryRunAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	v, err := a.automations.DryRun(r.Context(), r.PathValue("id"), automationOwner(p))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (a *API) runAutomation(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	v, err := a.automations.RunNow(r.Context(), r.PathValue("id"), automationOwner(p))
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"runs": v})
}
func (a *API) automationHistory(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	v, err := a.automations.History(r.Context(), r.PathValue("id"), automationOwner(p), 50)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"runs": v})
}

type blueprintInstallRequest struct {
	Name             string                `json:"name"`
	InputDriveID     string                `json:"inputDriveId"`
	OutputDriveID    string                `json:"outputDriveId"`
	ProfileID        string                `json:"profileId"`
	ProfileVersion   string                `json:"profileVersion"`
	TargetFormat     string                `json:"targetFormat"`
	Options          map[string]string     `json:"options"`
	Condition        *automation.Condition `json:"condition"`
	Extensions       []string              `json:"extensions"`
	NameGlob         string                `json:"nameGlob"`
	MinBytes         int64                 `json:"minBytes"`
	MaxBytes         int64                 `json:"maxBytes"`
	ProcessExisting  bool                  `json:"processExistingOnNextScan"`
	OutputBehavior   string                `json:"outputBehavior"`
	IntervalSeconds  int                   `json:"intervalSeconds"`
	StabilitySeconds int                   `json:"stabilitySeconds"`
	MaxConcurrent    int                   `json:"maxConcurrent"`
}

func (a *API) installBlueprint(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !a.automationService(w) {
		return
	}
	p, _ := a.identity(r)
	var req blueprintInstallRequest
	if !decodeBody(w, r, &req) {
		return
	}
	var template *automation.Blueprint
	for _, b := range automation.BuiltinBlueprints() {
		if b.ID == r.PathValue("id") {
			x := b
			template = &x
			break
		}
	}
	if template == nil {
		writeAPIError(w, 404, "blueprint_not_found", "blueprint not found")
		return
	}
	d := template.Definition
	d.Trigger.InputDriveID = req.InputDriveID
	d.Trigger.ProcessExistingOnNext = req.ProcessExisting
	if req.IntervalSeconds != 0 {
		d.Trigger.IntervalSeconds = req.IntervalSeconds
	}
	if req.StabilitySeconds != 0 {
		d.Trigger.StabilitySeconds = req.StabilitySeconds
	}
	if req.MaxConcurrent != 0 {
		d.Trigger.MaxConcurrent = req.MaxConcurrent
	}
	d.Filter.Extensions = req.Extensions
	d.Filter.NameGlob = req.NameGlob
	d.Filter.MinBytes = req.MinBytes
	d.Filter.MaxBytes = req.MaxBytes
	d.Condition = req.Condition
	d.Action.OutputDriveID = req.OutputDriveID
	d.Action.ProfileID = req.ProfileID
	d.Action.ProfileVersion = req.ProfileVersion
	d.Action.TargetFormat = req.TargetFormat
	d.Action.Options = req.Options
	d.Action.OutputBehavior = req.OutputBehavior
	if d.Action.OutputBehavior == "" {
		d.Action.OutputBehavior = "unique"
	}
	v, err := a.automations.CreateAutomation(r.Context(), p.ID, req.Name, d)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"automation": v, "blueprintId": template.ID, "blueprintVersion": template.Version})
}

func writeAutomationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrNotFound):
		writeAPIError(w, 404, "automation_not_found", "mounted drive or automation was not found")
	case errors.Is(err, automation.ErrConflict):
		writeAPIError(w, 409, "resource_in_use", "resource is still referenced by an automation or active run")
	default:
		msg := strings.TrimSpace(err.Error())
		if len(msg) > 350 {
			msg = msg[:350]
		}
		writeAPIError(w, 422, "automation_invalid", msg)
	}
}
