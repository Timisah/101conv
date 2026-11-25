package repositorymanager

import (
	"context"
	"testing"
)

func TestBatchInstallPlanDeduplicatesSharedEngineAndAppliesAtomically(t *testing.T) {
	_, repos, eng := setup(t)
	ctx := context.Background()

	preview, err := repos.CreateBatchInstallPlan(ctx, "trusted-test", []string{"video-one", "video-two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 2 {
		t.Fatalf("expected 2 planned profiles, got %d", len(preview.Items))
	}
	if len(preview.Dependencies) != 1 || preview.Dependencies[0].Action != "install engine" {
		t.Fatalf("expected 1 deduplicated engine dependency, got %+v", preview.Dependencies)
	}
	if preview.Publisher != "Trusted Test" || preview.Trust != TrustPinned {
		t.Fatalf("unexpected trust/publisher metadata: %+v", preview)
	}

	applied, err := repos.ApplyBatchInstallPlan(ctx, "trusted-test", preview.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied profiles, got %d", len(applied))
	}

	engines, err := eng.ListEngines(ctx)
	if err != nil || len(engines) != 1 {
		t.Fatalf("expected shared engine installed once, got %d (%v)", len(engines), err)
	}
	profiles, err := eng.ListProfiles(ctx)
	if err != nil || len(profiles) != 2 {
		t.Fatalf("expected 2 installed profiles, got %d (%v)", len(profiles), err)
	}

	// Re-planning the same already-installed profiles should report conflict.
	if _, err := repos.CreateBatchInstallPlan(ctx, "trusted-test", []string{"video-one", "video-two"}); err == nil {
		t.Fatal("expected error when all selected profiles are already up to date")
	}
}
