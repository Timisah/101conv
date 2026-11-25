package demo

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestDisabledDemoModeIsNoOp(t *testing.T) {
	svc := NewService(Policy{Enabled: false}, nil)
	if svc.Enabled() {
		t.Fatal("expected disabled demo service")
	}
	if status := svc.Status(); status.Enabled {
		t.Fatalf("expected disabled status, got %+v", status)
	}
	if err := svc.CheckUpload(context.Background(), 500<<20); err != nil {
		t.Fatalf("disabled demo should not block uploads: %v", err)
	}
	if err := svc.CheckSubmission(context.Background(), 999, "anything", 100); err != nil {
		t.Fatalf("disabled demo should not block submissions: %v", err)
	}
}

func TestDemoGuardsKillSwitchBudgetsAndAllowlist(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	svc := NewService(Policy{
		Enabled:              true,
		MaxFileBytes:         10 << 20,
		MaxBatchFiles:        2,
		MaxActiveJobs:        2,
		MaxGlobalJobsPerHour: 3,
		AllowedProfiles:      []string{"ffmpeg-image-linux-amd64", "jpeg-xl-linux-amd64"},
	}, nil)
	svc.now = func() time.Time { return now }

	// Upload size limit.
	if err := svc.CheckUpload(ctx, 5<<20); err != nil {
		t.Fatalf("expected 5 MiB upload to pass: %v", err)
	}
	if err := svc.CheckUpload(ctx, 20<<20); err == nil {
		t.Fatal("expected 20 MiB upload to be rejected")
	} else {
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Code != "demo_file_limit_exceeded" || rej.HTTPStatus != http.StatusRequestEntityTooLarge {
			t.Fatalf("unexpected upload error: %+v", err)
		}
	}

	// Profile allowlist.
	if err := svc.CheckSubmission(ctx, 0, "ffmpeg-linux-amd64", 1); err == nil {
		t.Fatal("expected non-allowlisted profile to be rejected")
	} else {
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Code != "demo_profile_not_allowed" || rej.HTTPStatus != http.StatusForbidden {
			t.Fatalf("unexpected profile rejection: %+v", err)
		}
	}

	// Batch limit.
	if err := svc.CheckSubmission(ctx, 0, "ffmpeg-image-linux-amd64", 3); err == nil {
		t.Fatal("expected batch > 2 to be rejected")
	}

	// Active jobs overload.
	if err := svc.CheckSubmission(ctx, 2, "ffmpeg-image-linux-amd64", 1); err == nil {
		t.Fatal("expected active job cap to reject")
	} else {
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Code != "demo_overload" || rej.HTTPStatus != http.StatusServiceUnavailable {
			t.Fatalf("unexpected overload rejection: %+v", err)
		}
	}

	// Hourly global budget.
	if err := svc.CheckSubmission(ctx, 0, "ffmpeg-image-linux-amd64", 2); err != nil {
		t.Fatalf("expected submission within hourly budget: %v", err)
	}
	svc.RecordSubmission(ctx, 2)
	if err := svc.CheckSubmission(ctx, 0, "ffmpeg-image-linux-amd64", 2); err == nil {
		t.Fatal("expected 2+2 > 3 hourly budget to reject")
	} else {
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Code != "demo_hourly_budget_exceeded" || rej.HTTPStatus != http.StatusTooManyRequests {
			t.Fatalf("unexpected budget rejection: %+v", err)
		}
	}

	// Advance clock by 61 minutes: sliding window resets.
	now = now.Add(61 * time.Minute)
	if err := svc.CheckSubmission(ctx, 0, "ffmpeg-image-linux-amd64", 1); err != nil {
		t.Fatalf("expected hourly budget to reset after 1 hour: %v", err)
	}

	// Runtime kill switch.
	status := svc.SetKillSwitch(true, "high CPU load")
	if !status.KillSwitchActive || status.KillSwitchReason != "high CPU load" {
		t.Fatalf("unexpected kill switch status: %+v", status)
	}
	if err := svc.CheckUpload(ctx, 1024); err == nil {
		t.Fatal("expected kill switch to block upload")
	}
	if err := svc.CheckSubmission(ctx, 0, "ffmpeg-image-linux-amd64", 1); err == nil {
		t.Fatal("expected kill switch to block submission")
	} else {
		var rej *RejectionError
		if !errors.As(err, &rej) || rej.Code != "demo_kill_switch_active" || rej.HTTPStatus != http.StatusServiceUnavailable {
			t.Fatalf("unexpected kill switch error: %+v", err)
		}
	}

	// Disable kill switch.
	status = svc.SetKillSwitch(false, "ignored")
	if status.KillSwitchActive || status.KillSwitchReason != "" {
		t.Fatalf("expected cleared kill switch, got %+v", status)
	}
}

func TestDemoFunnelMetricsAggregation(t *testing.T) {
	ctx := context.Background()
	svc := NewService(Policy{Enabled: true}, nil)
	if err := svc.RecordEvent(ctx, "arbitrary_untrusted_event"); err == nil {
		t.Fatal("expected unknown event to be rejected")
	}
	for i := 0; i < 4; i++ {
		svc.RecordSubmission(ctx, 1)
	}
	_ = svc.RecordEvent(ctx, EventCTAGitHubClick)
	_ = svc.RecordEvent(ctx, EventCTAInstallDocsClick)
	_ = svc.RecordEvent(ctx, EventCTAInstallDocsClick)

	metrics, err := svc.Metrics(ctx, 7)
	if err != nil {
		t.Fatalf("metrics error: %v", err)
	}
	if metrics.Totals[EventDemoConversionStarted] != 4 {
		t.Fatalf("expected 4 conversion starts, got %d", metrics.Totals[EventDemoConversionStarted])
	}
	if metrics.GitHubCTRPercent != 25 {
		t.Fatalf("expected 25%% GitHub CTR, got %v", metrics.GitHubCTRPercent)
	}
	if metrics.InstallDocsCTRPercent != 50 {
		t.Fatalf("expected 50%% install docs CTR, got %v", metrics.InstallDocsCTRPercent)
	}
}
