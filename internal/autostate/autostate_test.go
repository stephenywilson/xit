package autostate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadNoFile(t *testing.T) {
	tmp := t.TempDir()
	state, path, err := Read(filepath.Join(tmp, "project"), filepath.Join(tmp, "user"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if state != nil {
		t.Fatal("expected nil state")
	}
	if path != "" {
		t.Fatal("expected empty path")
	}
}

func TestReadProjectPriority(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")

	_ = os.MkdirAll(filepath.Join(userHome, "state"), 0755)
	_ = os.WriteFile(filepath.Join(userHome, "state", "current.json"), []byte(`{"status":"completed","saved_bytes":100}`), 0644)

	state, path, err := Read(projectHome, userHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state == nil {
		t.Fatal("expected state from user fallback")
	}
	if path != filepath.Join(userHome, "state", "current.json") {
		t.Fatalf("expected user path, got %s", path)
	}

	_ = os.MkdirAll(filepath.Join(projectHome, "state"), 0755)
	_ = os.WriteFile(filepath.Join(projectHome, "state", "current.json"), []byte(`{"status":"running","saved_bytes":200}`), 0644)

	state, path, err = Read(projectHome, userHome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != "running" {
		t.Fatalf("expected project priority, got status %s", state.Status)
	}
	if path != filepath.Join(projectHome, "state", "current.json") {
		t.Fatalf("expected project path, got %s", path)
	}
}

func TestIsRunningFresh(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	fresh := &AutoState{Status: "running", StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)}
	if !IsRunningFresh(fresh, now) {
		t.Error("expected fresh for 5min old running")
	}

	stale := &AutoState{Status: "running", StartedAt: now.Add(-11 * time.Minute).Format(time.RFC3339)}
	if IsRunningFresh(stale, now) {
		t.Error("expected stale for 11min old running")
	}

	completed := &AutoState{Status: "completed", StartedAt: now.Add(-1 * time.Minute).Format(time.RFC3339)}
	if IsRunningFresh(completed, now) {
		t.Error("expected not running for completed state")
	}

	if IsRunningFresh(nil, now) {
		t.Error("expected not running for nil state")
	}
}

func TestIsCompletedFresh(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	fresh := &AutoState{Status: "completed", FinishedAt: now.Add(-10 * time.Second).Format(time.RFC3339), SavedBytes: 1000}
	if !IsCompletedFresh(fresh, now) {
		t.Error("expected fresh for 10s old completed")
	}

	stale := &AutoState{Status: "completed", FinishedAt: now.Add(-35 * time.Second).Format(time.RFC3339), SavedBytes: 1000}
	if IsCompletedFresh(stale, now) {
		t.Error("expected stale for 35s old completed")
	}

	running := &AutoState{Status: "running", FinishedAt: now.Add(-1 * time.Second).Format(time.RFC3339)}
	if IsCompletedFresh(running, now) {
		t.Error("expected not completed for running state")
	}

	if IsCompletedFresh(nil, now) {
		t.Error("expected not completed for nil state")
	}
}

func TestReadMalformedJSON(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(projectHome, "state"), 0755)
	_ = os.WriteFile(filepath.Join(projectHome, "state", "current.json"), []byte(`{not json`), 0644)

	state, _, err := Read(projectHome, "")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if state != nil {
		t.Fatal("expected nil state for malformed JSON")
	}
}

func TestRunningStaleOver10Min(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	stale := &AutoState{Status: "running", StartedAt: now.Add(-10 * time.Minute).Add(-1 * time.Second).Format(time.RFC3339)}
	if IsRunningFresh(stale, now) {
		t.Error("expected stale for running over 10min")
	}
}

func TestCompletedStaleOver30s(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	stale := &AutoState{Status: "completed", FinishedAt: now.Add(-30 * time.Second).Add(-1 * time.Second).Format(time.RFC3339), SavedBytes: 5000}
	if IsCompletedFresh(stale, now) {
		t.Error("expected stale for completed over 30s")
	}
}
