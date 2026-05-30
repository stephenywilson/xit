package integrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/config"
)

func withFakePath(t *testing.T, names ...string) string {
	dir := t.TempDir()
	for _, name := range names {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("#!/bin/sh\necho fake"), 0755)
	}
	t.Setenv("PATH", dir)
	return dir
}

func TestDetectAllFindsFakeTargets(t *testing.T) {
	withFakePath(t, "claude", "codex", "kimi", "minimax", "cursor")
	m := DetectAll(nil)

	for _, name := range []string{"claude", "codex", "kimi", "minimax", "cursor"} {
		s, ok := m[name]
		if !ok {
			t.Fatalf("missing detection for %s", name)
		}
		if !s.Detected {
			t.Errorf("expected %s detected", name)
		}
		if s.Path == "" {
			t.Errorf("expected %s path non-empty", name)
		}
	}
}

func TestMiniMaxDetectsMmx(t *testing.T) {
	dir := withFakePath(t, "mmx")
	m := DetectAll(nil)
	if !m["minimax"].Detected {
		t.Errorf("expected minimax detected via mmx")
	}
	expected := filepath.Join(dir, "mmx")
	if m["minimax"].Path != expected {
		t.Errorf("expected path %s, got %s", expected, m["minimax"].Path)
	}
}

func TestDetectAllMissesMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := DetectAll(nil)
	for _, name := range []string{"claude", "codex", "kimi", "minimax", "cursor"} {
		if m[name].Detected {
			t.Errorf("expected %s not detected", name)
		}
	}
}

func TestClaudePlanRecommendedHook(t *testing.T) {
	withFakePath(t, "claude")
	a := Registry["claude"]
	plan := a.PlanInstall("", config.Default("0.2.6"), "")

	if plan.Target != "claude" {
		t.Errorf("expected target claude, got %s", plan.Target)
	}
	if plan.RecommendedMethod != MethodOfficialHook {
		t.Errorf("expected recommended official_hook, got %s", plan.RecommendedMethod)
	}
	if !plan.Detected {
		t.Error("expected detected")
	}
	if !plan.CanInstall {
		t.Error("official_hook should be installable for claude in v0.2.6")
	}
	if !strings.Contains(plan.Note, "observe mode") {
		t.Errorf("expected note about observe mode, got: %s", plan.Note)
	}
	if !strings.Contains(strings.Join(plan.Actions, " "), "logs events") {
		t.Error("expected plan to mention logs events")
	}
}

func TestClaudeFallbackMethods(t *testing.T) {
	withFakePath(t, "claude")
	a := Registry["claude"]
	plan := a.PlanInstall("", config.Default("0.2.5"), "")

	hasSessionShim := false
	hasWrapper := false
	for _, m := range plan.SupportedMethods {
		if m == MethodSessionShim {
			hasSessionShim = true
		}
		if m == MethodWrapper {
			hasWrapper = true
		}
	}
	if !hasSessionShim {
		t.Error("expected fallback to include session_shim")
	}
	if !hasWrapper {
		t.Error("expected fallback to include wrapper")
	}
}

func TestCodexPlanRecommendedHook(t *testing.T) {
	withFakePath(t, "codex")
	a := Registry["codex"]
	plan := a.PlanInstall("", config.Default("0.2.5"), "")

	if plan.RecommendedMethod != MethodOfficialHook {
		t.Errorf("expected recommended official_hook, got %s", plan.RecommendedMethod)
	}
	if plan.CanInstall {
		t.Error("official_hook should not be installable")
	}
	if !strings.Contains(plan.Note, "not enabled in this build") {
		t.Errorf("expected note about not enabled, got: %s", plan.Note)
	}
}

func TestKimiPlanNoteCaution(t *testing.T) {
	withFakePath(t, "kimi")
	a := Registry["kimi"]
	plan := a.PlanInstall("", config.Default("0.2.8"), "")

	if plan.RecommendedMethod != MethodManual {
		t.Errorf("expected recommended manual, got %s", plan.RecommendedMethod)
	}
	status := a.Detect()
	if !strings.Contains(status.Note, "not currently safe") || !strings.Contains(status.Note, "unsafe-pty") {
		t.Errorf("expected adapter note about TUI safety, got: %s", status.Note)
	}
	if !strings.Contains(status.Note, "beta") {
		t.Errorf("expected adapter note about beta hooks, got: %s", status.Note)
	}
	if plan.CanInstall {
		t.Error("kimi manual should not be installable via PlanInstall")
	}
	// Manual method has no safe option; wrapper method does.
	wrapperPlan := a.PlanInstall("", config.Default("0.2.8"), "wrapper")
	if !strings.Contains(wrapperPlan.SafeOption, "wrapper") {
		t.Errorf("expected wrapper safe option, got: %s", wrapperPlan.SafeOption)
	}
}

func TestMiniMaxDoesNotOverclaimHook(t *testing.T) {
	withFakePath(t, "minimax")
	a := Registry["minimax"]
	plan := a.PlanInstall("", config.Default("0.2.5"), "")

	for _, m := range plan.SupportedMethods {
		if m == MethodOfficialHook {
			t.Error("minimax should not claim official_hook support")
		}
	}
	if plan.RecommendedMethod == MethodOfficialHook {
		t.Error("minimax recommended method should not be official_hook")
	}
	if !strings.Contains(plan.Note, "do not overclaim") {
		t.Errorf("expected note about not overclaiming, got: %s", plan.Note)
	}
}

func TestMiniMaxMmxDetected(t *testing.T) {
	withFakePath(t, "mmx")
	a := Registry["minimax"]
	status := a.Detect()
	if !status.Detected {
		t.Error("expected minimax detected via mmx")
	}
}

func TestCursorPlanDependsOnEnvironment(t *testing.T) {
	withFakePath(t, "cursor")
	a := Registry["cursor"]
	plan := a.PlanInstall("", config.Default("0.2.5"), "")

	if !strings.Contains(plan.Note, "depend on") || !strings.Contains(plan.Note, "context") {
		t.Errorf("expected note about dependency/context, got: %s", plan.Note)
	}
	if plan.RecommendedMethod == MethodOfficialHook {
		t.Error("cursor should not universally recommend official_hook")
	}
}

func TestPlanInstallDoesNotWriteConfig(t *testing.T) {
	withFakePath(t, "claude")
	a := Registry["claude"]
	home := t.TempDir()
	cfg := config.Default("0.2.6")
	plan := a.PlanInstall(home, cfg, "")

	_, err := os.Stat(filepath.Join(home, "config.json"))
	if err == nil {
		t.Error("PlanInstall should not create config.json")
	}

	if plan.Target != "claude" {
		t.Errorf("expected target claude, got %s", plan.Target)
	}
}

func TestOfficialHookInstallReturnsError(t *testing.T) {
	withFakePath(t, "codex")
	a := Registry["codex"]
	home := t.TempDir()
	cfg := config.Default("0.2.6")
	plan := a.PlanInstall(home, cfg, "")

	err := a.Install(home, cfg, plan, true)
	if err == nil {
		t.Fatal("expected install error for codex official_hook")
	}
	if !strings.Contains(err.Error(), "not installable") {
		t.Errorf("expected 'not installable' error, got: %v", err)
	}
}

func TestWrapperInstallWritesConfig(t *testing.T) {
	withFakePath(t, "kimi")
	a := Registry["kimi"]
	home := t.TempDir()
	cfg := config.Default("0.2.5")
	plan := a.PlanInstall(home, cfg, "wrapper")

	if !plan.CanInstall {
		t.Fatal("wrapper should be installable")
	}

	err := a.Install(home, cfg, plan, true)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	loaded, err := config.Load(home)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if !loaded.Targets["kimi"].Enabled {
		t.Error("kimi should be enabled after wrapper install")
	}
	if loaded.Targets["kimi"].Integration != "wrapper" {
		t.Errorf("expected integration wrapper, got %s", loaded.Targets["kimi"].Integration)
	}
}

func TestWrapperInstallRequiresYes(t *testing.T) {
	withFakePath(t, "kimi")
	a := Registry["kimi"]
	home := t.TempDir()
	cfg := config.Default("0.2.5")
	plan := a.PlanInstall(home, cfg, "wrapper")

	err := a.Install(home, cfg, plan, false)
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if !strings.Contains(err.Error(), "requires --yes") {
		t.Errorf("expected --yes error, got: %v", err)
	}
}

func TestDoctorResultInstalled(t *testing.T) {
	withFakePath(t, "claude")
	a := Registry["claude"]
	cfg := config.Default("0.2.5")
	cfg.Targets["claude"] = config.Target{Enabled: true, Integration: "wrapper"}

	result := a.Doctor(cfg)
	if result.Installed != "wrapper" {
		t.Errorf("expected installed wrapper, got %s", result.Installed)
	}
}

func TestDoctorResultNotInstalled(t *testing.T) {
	withFakePath(t, "claude")
	a := Registry["claude"]
	cfg := config.Default("0.2.5")

	result := a.Doctor(cfg)
	if result.Installed != "no" {
		t.Errorf("expected installed no, got %s", result.Installed)
	}
}

func TestAllAdaptersOrder(t *testing.T) {
	adapters := AllAdapters()
	expected := []string{"claude", "codex", "cursor", "kimi", "minimax"}
	if len(adapters) != len(expected) {
		t.Fatalf("expected %d adapters, got %d", len(expected), len(adapters))
	}
	for i, name := range expected {
		if adapters[i].Name() != name {
			t.Errorf("expected adapter %d to be %s, got %s", i, name, adapters[i].Name())
		}
	}
}
