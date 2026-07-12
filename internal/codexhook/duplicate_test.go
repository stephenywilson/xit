package codexhook

import (
	"os"
	"testing"
)

// TestCheckDuplicateHook_NoneInstalled: fresh project and fresh (fake) HOME,
// neither layer installed -> no duplicate.
func TestCheckDuplicateHook_NoneInstalled(t *testing.T) {
	projectPath := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	check, err := CheckDuplicateHook(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if check.ProjectLevelInstalled || check.UserLevelInstalled || check.Duplicate() {
		t.Fatalf("expected no hooks installed anywhere, got %+v", check)
	}
}

// TestCheckDuplicateHook_ProjectOnly: the common case (this repo's own
// setup) — installed at project level only, must NOT be flagged duplicate.
func TestCheckDuplicateHook_ProjectOnly(t *testing.T) {
	projectPath := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	xitHome := t.TempDir()

	if _, err := Install(projectPath, xitHome, false); err != nil {
		t.Fatal(err)
	}

	check, err := CheckDuplicateHook(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !check.ProjectLevelInstalled {
		t.Fatal("expected project-level hook to be detected as installed")
	}
	if check.UserLevelInstalled {
		t.Fatal("expected no user-level hook in a fresh fake HOME")
	}
	if check.Duplicate() {
		t.Fatal("project-only install must never be reported as a duplicate")
	}
}

// TestCheckDuplicateHook_BothLayers: install at both project AND the Codex
// user-level layer (~/.codex/hooks.json) — must be flagged as a real
// duplicate, since Codex fires both concurrently for the same project.
func TestCheckDuplicateHook_BothLayers(t *testing.T) {
	projectPath := t.TempDir()
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	xitHome := t.TempDir()

	if _, err := Install(projectPath, xitHome, false); err != nil {
		t.Fatal(err)
	}
	// Install the SAME hook set at the Codex user-level layer by treating
	// fakeHome itself as the "project path" for ReadHooksConfig/WriteHooksConfig
	// (this is exactly how codexUserConfigDir()+ReadHooksConfig("<home>") reads
	// ~/.codex/hooks.json — same convention, just simulated here).
	if _, err := Install(fakeHome, xitHome, false); err != nil {
		t.Fatal(err)
	}

	check, err := CheckDuplicateHook(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Duplicate() {
		t.Fatalf("expected both-layers install to be flagged as duplicate, got %+v", check)
	}
}

// TestCodexUserConfigDir_UsesHomeEnv confirms the resolver honors HOME
// (needed so tests never touch the real developer machine's ~/.codex).
func TestCodexUserConfigDir_UsesHomeEnv(t *testing.T) {
	t.Setenv("HOME", "/tmp/xit-fake-home-for-test")
	if got := codexUserConfigDir(); got != "/tmp/xit-fake-home-for-test" {
		t.Fatalf("codexUserConfigDir() = %q, want the HOME env value", got)
	}
	_ = os.Getenv("HOME") // sanity: env var actually set by t.Setenv
}
