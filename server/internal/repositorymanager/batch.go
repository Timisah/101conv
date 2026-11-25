package repositorymanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

type batchProfilePlan struct {
	profile    profile.Definition
	update     bool
	selections map[string]string
}

type batchInstallationPlan struct {
	preview           BatchInstallPreview
	items             []batchProfilePlan
	engineDefinitions map[string]engine.Definition
	installedDigest   string
	expires           time.Time
}

func filterValidBundles(bundles []StarterBundle) []StarterBundle {
	if len(bundles) == 0 {
		return nil
	}
	out := make([]StarterBundle, 0, len(bundles))
	for _, b := range bundles {
		id := strings.TrimSpace(b.ID)
		name := strings.TrimSpace(b.Name)
		if id == "" || name == "" || len(b.ProfileIDs) == 0 {
			continue
		}
		seen := map[string]bool{}
		ids := make([]string, 0, len(b.ProfileIDs))
		for _, pid := range b.ProfileIDs {
			pid = strings.TrimSpace(pid)
			if pid == "" || seen[pid] {
				continue
			}
			seen[pid] = true
			ids = append(ids, pid)
		}
		if len(ids) == 0 {
			continue
		}
		out = append(out, StarterBundle{
			ID:          id,
			Name:        name,
			Description: strings.TrimSpace(b.Description),
			ProfileIDs:  ids,
		})
	}
	return out
}

// CreateBatchInstallPlan builds one deduplicated pre-approval plan covering
// multiple profile installs and/or updates from a single repository. Shared
// engine dependencies are resolved and downloaded at most once.
func (m *Manager) CreateBatchInstallPlan(ctx context.Context, repoID string, profileIDs []string) (BatchInstallPreview, error) {
	if len(profileIDs) == 0 {
		return BatchInstallPreview{}, fmt.Errorf("at least one profile id is required")
	}
	if len(profileIDs) > 32 {
		return BatchInstallPreview{}, fmt.Errorf("at most 32 profiles can be planned together")
	}
	uniqueIDs := make([]string, 0, len(profileIDs))
	seenIDs := map[string]bool{}
	for _, raw := range profileIDs {
		id := strings.TrimSpace(raw)
		if id == "" || seenIDs[id] {
			continue
		}
		seenIDs[id] = true
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return BatchInstallPreview{}, fmt.Errorf("at least one valid profile id is required")
	}

	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	s, err := m.getSource(repoID)
	if err != nil {
		return BatchInstallPreview{}, err
	}
	if !s.Enabled {
		return BatchInstallPreview{}, ErrRepositoryDisabled
	}
	unsignedApproval := s.ID == "101conv-official" && s.Trust == TrustApprovedUnsigned
	if s.Trust != TrustPinned && !unsignedApproval {
		return BatchInstallPreview{}, fmt.Errorf("repository %q is not approved for installation", s.ID)
	}
	m.tufMu.Lock()
	var view verifiedView
	if unsignedApproval {
		view, err = loadUnsignedView(ctx, s, officialURL, m.appVersion)
	} else {
		view, err = m.loadViewLocked(ctx, s)
	}
	m.tufMu.Unlock()
	if err != nil {
		return BatchInstallPreview{}, errors.Join(err, m.recordRefresh(s.ID, err))
	}
	if err = m.recordRefresh(s.ID, nil); err != nil {
		return BatchInstallPreview{}, fmt.Errorf("persist repository status: %w", err)
	}

	installedProfiles, err := m.engines.ListProfiles(ctx)
	if err != nil {
		return BatchInstallPreview{}, err
	}
	installedByID := make(map[string]management.ProfileRecord, len(installedProfiles))
	for _, rec := range installedProfiles {
		installedByID[rec.Definition.ID] = rec
	}
	installedEngines, err := m.engines.ListEngines(ctx)
	if err != nil {
		return BatchInstallPreview{}, err
	}

	preview := BatchInstallPreview{
		CatalogDigest:  view.digest,
		Repository:     view.catalog.Repository,
		Publisher:      view.catalog.Repository.Name,
		Trust:          view.catalog.Repository.Trust,
		Items:          []BatchProfileItem{},
		Dependencies:   []DependencyPreview{},
		PlannedChanges: []string{},
	}
	planItems := make([]batchProfilePlan, 0, len(uniqueIDs))
	engineDefinitions := map[string]engine.Definition{}
	depIndexByKey := map[string]int{}

	for _, profileID := range uniqueIDs {
		p, ok := highestProfile(view.catalog.Profiles, profileID)
		if !ok {
			for _, incompatible := range view.catalog.IncompatibleProfiles {
				if incompatible.ID == profileID {
					return BatchInstallPreview{}, coded("incompatible_version", fmt.Errorf("%w: %s", ErrIncompatible, incompatible.Reason))
				}
			}
			return BatchInstallPreview{}, management.ErrNotFound
		}
		current, exists := installedByID[profileID]
		update := false
		preferred := map[string]string{}
		if exists {
			if !newer(p.Version, current.Definition.Version) {
				// Already installed at this or a newer version; skip in batch.
				continue
			}
			update = true
			preferred = current.State.ResolvedEngines
		}

		selections := make(map[string]string, len(p.Engines))
		for _, req := range p.Engines {
			dep := DependencyPreview{EngineID: req.EngineID, VersionRange: req.Versions, Capabilities: req.Capabilities, Action: "install engine"}
			for _, cur := range installedEngines {
				if cur.Installation.Version == preferred[req.EngineID] && req.Accepts(cur.Definition) && profileExecutablesAvailable(p, cur.Installation) {
					dep.InstalledVersion = cur.Installation.Version
					dep.EngineName = cur.Definition.Name
					dep.EngineLicense = cur.Definition.License
					dep.Action = "reuse installed engine"
					break
				}
			}
			if dep.InstalledVersion == "" {
				for _, cur := range installedEngines {
					if req.Accepts(cur.Definition) && profileExecutablesAvailable(p, cur.Installation) && (dep.InstalledVersion == "" || newer(cur.Installation.Version, dep.InstalledVersion)) {
						dep.InstalledVersion = cur.Installation.Version
						dep.EngineName = cur.Definition.Name
						dep.EngineLicense = cur.Definition.License
					}
				}
			}
			if dep.InstalledVersion != "" {
				dep.Action = "reuse installed engine"
				selections[req.EngineID] = dep.InstalledVersion
			} else {
				best := ""
				for _, d := range view.catalog.Engines {
					if req.Accepts(d) && platformCompatible(d.Platform) && profileExecutablesDeclared(p, d) && (best == "" || newer(d.Version, best)) {
						best = d.Version
						dep.EngineName = d.Name
						dep.EngineLicense = d.License
					}
				}
				if best == "" {
					return BatchInstallPreview{}, coded("missing_dependency", fmt.Errorf("%w: profile %q needs engine %q range [%s,%s) and capabilities %v, but no compatible engine is available", ErrMissingDependency, p.ID, req.EngineID, req.Versions.Minimum, req.Versions.MaximumExclusive, req.Capabilities))
				}
				selections[req.EngineID] = best
				engKey := req.EngineID + "\x00" + best
				if _, alreadyPlanned := engineDefinitions[engKey]; !alreadyPlanned {
					for _, def := range view.catalog.Engines {
						if def.ID == req.EngineID && def.Version == best {
							engineDefinitions[engKey] = def
						}
					}
					preview.PlannedChanges = append(preview.PlannedChanges, fmt.Sprintf("install engine %s %s", req.EngineID, best))
				}
			}
			depKey := dep.EngineID + "\x00" + selections[req.EngineID] + "\x00" + dep.Action
			if _, seenDep := depIndexByKey[depKey]; !seenDep {
				depIndexByKey[depKey] = len(preview.Dependencies)
				preview.Dependencies = append(preview.Dependencies, dep)
			}
		}

		item := BatchProfileItem{
			Profile: p,
			Action:  "install",
			License: p.License,
		}
		if update {
			item.Action = "update"
			item.InstalledVersion = current.Definition.Version
			item.ProfileChanges = DescribeProfileChanges(current.Definition, p)
			preview.PlannedChanges = append(preview.PlannedChanges, fmt.Sprintf("update profile %s %s -> %s", p.ID, current.Definition.Version, p.Version))
		} else {
			preview.PlannedChanges = append(preview.PlannedChanges, fmt.Sprintf("install profile %s %s", p.ID, p.Version))
		}
		preview.Items = append(preview.Items, item)
		planItems = append(planItems, batchProfilePlan{profile: p, update: update, selections: selections})
	}

	if len(planItems) == 0 {
		return BatchInstallPreview{}, fmt.Errorf("all selected profiles are already up to date: %w", management.ErrConflict)
	}

	installedDigest, err := m.engines.SnapshotDigest()
	if err != nil {
		return BatchInstallPreview{}, err
	}
	planID, err := randomPlanID()
	if err != nil {
		return BatchInstallPreview{}, err
	}
	expires := time.Now().UTC().Add(10 * time.Minute)
	preview.PlanID = planID
	preview.ExpiresAt = expires.Format(time.RFC3339)
	storedPreview, err := cloneJSON(preview)
	if err != nil {
		return BatchInstallPreview{}, err
	}

	m.mu.Lock()
	m.prunePlansLocked(time.Now())
	for id, bp := range m.batchPlans {
		if time.Now().After(bp.expires) {
			delete(m.batchPlans, id)
		}
	}
	m.batchPlans[planID] = batchInstallationPlan{
		preview:           storedPreview,
		items:             planItems,
		engineDefinitions: engineDefinitions,
		installedDigest:   installedDigest,
		expires:           expires,
	}
	m.mu.Unlock()
	return cloneJSON(preview)
}

// ApplyBatchInstallPlan downloads each unique required engine once and installs
// or updates all planned profiles in order.
func (m *Manager) ApplyBatchInstallPlan(ctx context.Context, repoID, planID string) ([]management.ProfileRecord, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	plan, ok := m.batchPlans[planID]
	if ok && time.Now().After(plan.expires) {
		delete(m.batchPlans, planID)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return nil, ErrPlanExpired
	}
	if plan.preview.Repository.ID != repoID {
		return nil, ErrStalePlan
	}
	source, err := m.getSource(repoID)
	if err != nil {
		return nil, err
	}
	if !source.Enabled {
		return nil, ErrRepositoryDisabled
	}
	unsignedApproval := source.ID == "101conv-official" && source.Trust == TrustApprovedUnsigned
	if source.Trust != TrustPinned && !unsignedApproval {
		return nil, fmt.Errorf("repository approval was removed or trust is unavailable")
	}
	stateDigest, err := m.engines.SnapshotDigest()
	if err != nil {
		return nil, err
	}
	if stateDigest != plan.installedDigest {
		return nil, ErrStalePlan
	}

	m.tufMu.Lock()
	var view verifiedView
	if unsignedApproval {
		view, err = loadUnsignedView(ctx, source, officialURL, m.appVersion)
	} else {
		view, err = m.loadViewLocked(ctx, source)
	}
	if err != nil {
		m.tufMu.Unlock()
		return nil, errors.Join(err, m.recordRefresh(source.ID, err))
	}
	if view.digest != plan.preview.CatalogDigest {
		m.tufMu.Unlock()
		return nil, errors.Join(ErrStalePlan, m.recordRefresh(source.ID, nil))
	}

	stagedByKey := map[string]management.VerifiedEnginePackage{}
	var cleanupPaths []string
	cleanup := func() error {
		var errs []error
		for i := len(cleanupPaths) - 1; i >= 0; i-- {
			if e := os.RemoveAll(cleanupPaths[i]); e != nil {
				errs = append(errs, fmt.Errorf("clean package staging %s: %w", cleanupPaths[i], e))
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}

	for key, def := range plan.engineDefinitions {
		if def.Artifact == nil {
			err = fmt.Errorf("planned engine %q has no artifact definition", def.ID)
			break
		}
		var archive []byte
		var e error
		if unsignedApproval {
			archive, e = downloadPackageUnsigned(ctx, def.Artifact, maxPackageBytes)
		} else {
			archive, e = downloadPackageVerified(ctx, view.updater, def.Artifact, maxPackageBytes)
		}
		if e != nil {
			err = e
			break
		}
		sum := sha256.Sum256(archive)
		digest := hex.EncodeToString(sum[:])
		if !strings.EqualFold(digest, def.Artifact.SHA256) {
			if unsignedApproval {
				err = coded("package_integrity_failure", fmt.Errorf("package digest changed for engine %q", def.ID))
			} else {
				err = coded("signature_failure", fmt.Errorf("%w: verified artifact digest changed for engine %q", ErrSignature, def.ID))
			}
			break
		}
		stage, _, e := m.engines.CreateVerifiedStaging()
		if e != nil {
			err = e
			break
		}
		cleanupPaths = append(cleanupPaths, stage)
		if e = extractArchive(archive, def.Artifact.Format, stage); e != nil {
			err = e
			break
		}
		stagedByKey[key] = management.VerifiedEnginePackage{
			Definition:   def,
			SourceDir:    stage,
			ArtifactHash: digest,
			RepositoryID: source.ID,
		}
	}
	m.tufMu.Unlock()
	if err != nil {
		return nil, errors.Join(err, cleanup())
	}

	// Commit each profile in order, passing only the staged engines it requires
	// that have not yet been committed by an earlier profile in this batch.
	committedEngines := map[string]bool{}
	results := make([]management.ProfileRecord, 0, len(plan.items))
	currentDigest := stateDigest
	for _, item := range plan.items {
		pkgs := []management.VerifiedEnginePackage{}
		for _, req := range item.profile.Engines {
			ver := item.selections[req.EngineID]
			key := req.EngineID + "\x00" + ver
			if pkg, staged := stagedByKey[key]; staged && !committedEngines[key] {
				pkgs = append(pkgs, pkg)
				committedEngines[key] = true
			}
		}
		rec, installErr := m.engines.InstallVerifiedProfile(item.profile, source.ID, pkgs, item.selections, currentDigest, item.update)
		if installErr != nil {
			return results, errors.Join(installErr, cleanup())
		}
		results = append(results, rec)
		nextDigest, digestErr := m.engines.SnapshotDigest()
		if digestErr != nil {
			return results, errors.Join(digestErr, cleanup())
		}
		currentDigest = nextDigest
	}

	m.mu.Lock()
	delete(m.batchPlans, planID)
	m.mu.Unlock()
	if cleanupErr := cleanup(); cleanupErr != nil {
		return results, fmt.Errorf("batch install committed; staging cleanup incomplete: %w", cleanupErr)
	}
	return results, nil
}
