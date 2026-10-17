package httpapi

import (
	"net/http"
	"time"
)

func (a *API) operationsOverview(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	operations, err := a.jobs.Operations(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "database_unavailable", "PostgreSQL is unavailable; check the database container and its persistent volume")
		return
	}
	storage, storageReady := a.checkSetupStorage()
	engines, err := a.management.ListEngines(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "management_error", "engine state could not be read")
		return
	}
	profiles, err := a.management.ListProfiles(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "management_error", "profile state could not be read")
		return
	}
	usable := 0
	for _, p := range profiles {
		if !p.State.Enabled {
			continue
		}
		for _, rule := range p.Definition.Rules {
			if len(rule.Sources) == 0 || len(rule.Targets) == 0 {
				continue
			}
			if _, _, _, _, resolveErr := a.management.ResolveConversion(p.Definition.ID, rule.Sources[0], rule.Targets[0]); resolveErr == nil {
				usable++
				break
			}
		}
	}
	workerOnline := false
	for _, worker := range operations.Workers {
		if worker.Online {
			workerOnline = true
			break
		}
	}
	free, freeErr := a.jobs.ResultFreeBytes()
	spaceReady := freeErr == nil && free >= 32<<20
	repositories := []map[string]any{}
	if a.repositories != nil {
		for _, source := range a.repositories.List() {
			repositories = append(repositories, map[string]any{"id": source.ID, "name": source.Name, "status": source.Status, "trust": source.Trust, "enabled": source.Enabled, "lastRefresh": source.LastRefresh, "lastError": source.LastError})
		}
	}
	issues := []string{}
	if !storageReady {
		issues = append(issues, "Core storage is not writable; check mounted directory ownership and available space.")
	}
	if !workerOnline {
		issues = append(issues, "No Worker heartbeat in the last 30 seconds; check the Worker container and database connection.")
	}
	if usable == 0 {
		issues = append(issues, "No enabled profile resolves to an installed engine; install or enable a compatible engine and profile.")
	}
	if !spaceReady {
		issues = append(issues, "Results storage has less than 32 MiB free or its capacity cannot be measured.")
	}
	if operations.ExpiredLeases > 0 {
		issues = append(issues, "Running jobs have expired leases; check Worker health and allow lease recovery.")
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"core": "running", "readyToConvert": storageReady && workerOnline && usable > 0 && spaceReady, "databaseConnected": operations.DatabaseConnected, "workers": operations.Workers, "jobs": operations.JobStates, "expiredLeases": operations.ExpiredLeases, "storage": storage, "resultFreeBytes": free, "installedEngines": len(engines), "installedProfiles": len(profiles), "usableProfiles": usable, "repositories": repositories, "recentCleanup": operations.RecentCleanup, "issues": issues, "checkedAt": time.Now().UTC()})
}
