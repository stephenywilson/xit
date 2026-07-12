package codexhook

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const testPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>%s</string>
	<key>CFBundleShortVersionString</key>
	<string>%s</string>
</dict>
</plist>
`

func writeFixtureApp(t *testing.T, bundleID, version string) string {
	t.Helper()
	appDir := filepath.Join(t.TempDir(), "Fixture.app")
	contentsDir := filepath.Join(appDir, "Contents")
	if err := os.MkdirAll(contentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	plist := []byte(fmt.Sprintf(testPlistTemplate, bundleID, version))
	if err := os.WriteFile(filepath.Join(contentsDir, "Info.plist"), plist, 0644); err != nil {
		t.Fatal(err)
	}
	return appDir
}

// TestDetectChatGPTApp_Absent covers "app not found" — must never error,
// never guess, and must report Installed=false.
func TestDetectChatGPTApp_Absent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "DoesNotExist.app")
	info := DetectChatGPTAppAt(missing)
	if info.Installed {
		t.Fatal("expected Installed=false for a missing app bundle")
	}
	if info.IsChatGPTCodexApp() {
		t.Fatal("a missing app must never report IsChatGPTCodexApp()=true")
	}
}

// TestDetectChatGPTApp_MatchingBundle covers the real ChatGPT desktop bundle
// id (com.openai.codex, confirmed on-machine via plutil against the actual
// installed app) — exercises the real plutil-based parser against a fixture.
func TestDetectChatGPTApp_MatchingBundle(t *testing.T) {
	app := writeFixtureApp(t, "com.openai.codex", "26.707.41301")
	info := DetectChatGPTAppAt(app)
	if !info.Installed {
		t.Fatal("expected Installed=true")
	}
	if info.BundleID != "com.openai.codex" {
		t.Fatalf("bundle id = %q, want com.openai.codex", info.BundleID)
	}
	if info.Version != "26.707.41301" {
		t.Fatalf("version = %q, want 26.707.41301", info.Version)
	}
	if !info.IsChatGPTCodexApp() {
		t.Fatal("expected IsChatGPTCodexApp()=true for the confirmed bundle id")
	}
}

// TestDetectChatGPTApp_UnrelatedBundle: an app happening to live at the
// fixture path with some OTHER bundle id must never be misreported as the
// ChatGPT Codex app — no guessing from the path alone.
func TestDetectChatGPTApp_UnrelatedBundle(t *testing.T) {
	app := writeFixtureApp(t, "com.example.SomethingElse", "1.0")
	info := DetectChatGPTAppAt(app)
	if !info.Installed {
		t.Fatal("expected Installed=true (the bundle exists)")
	}
	if info.IsChatGPTCodexApp() {
		t.Fatal("an unrelated bundle id must never report IsChatGPTCodexApp()=true")
	}
}

// TestChatGPTSharesCanonicalCodexHook is the core "no duplicate hook"
// guarantee: installing once, then checking status/re-installing as if from
// the "chatgpt" alias, must resolve to the exact same hooks.json — never a
// second file or a second set of entries.
func TestChatGPTSharesCanonicalCodexHook(t *testing.T) {
	projectPath := t.TempDir()
	home := t.TempDir()

	// First "install" (simulating `xit hook install codex --yes`).
	res1, err := Install(projectPath, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if res1.AlreadyInstalled {
		t.Fatal("first install should not report AlreadyInstalled")
	}

	// "chatgpt" status must read the identical canonical file — same path,
	// same installed state — because it calls the exact same Status().
	status, err := Status(projectPath, home)
	if err != nil {
		t.Fatal(err)
	}
	if status.HooksPath != res1.HooksPath {
		t.Fatalf("chatgpt/codex must share one hooks path: install=%q status=%q", res1.HooksPath, status.HooksPath)
	}
	if !status.Installed {
		t.Fatal("expected shared hook to be installed")
	}

	rawBefore, err := os.ReadFile(status.HooksPath)
	if err != nil {
		t.Fatal(err)
	}

	// Second "install" (simulating `xit hook install chatgpt --yes` run
	// afterward) must recognize it's already compatible and must NOT add a
	// duplicate hook entry — the file content must be byte-identical.
	res2, err := Install(projectPath, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.AlreadyInstalled {
		t.Fatal("re-install (simulating the chatgpt alias) should report AlreadyInstalled=true — no duplicate hook")
	}
	rawAfter, err := os.ReadFile(status.HooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(rawBefore) != string(rawAfter) {
		t.Fatal("re-install must not modify hooks.json when already installed (would indicate a duplicate hook entry)")
	}

	// Uninstall removes the ONE shared hook — must fully clear it so a
	// subsequent Status() (whichever alias asks) reports not-installed.
	if err := Uninstall(projectPath); err != nil {
		t.Fatal(err)
	}
	statusAfterUninstall, err := Status(projectPath, home)
	if err != nil {
		t.Fatal(err)
	}
	if statusAfterUninstall.Installed {
		t.Fatal("expected hook to be fully removed after uninstall — no leftover shared state")
	}
}

// TestChatGPTStatusWithoutInstall: app present but hook not installed must
// give a clear, non-crashing "not installed" status rather than a fabricated
// positive result.
func TestChatGPTStatusWithoutInstall(t *testing.T) {
	projectPath := t.TempDir()
	home := t.TempDir()
	status, err := Status(projectPath, home)
	if err != nil {
		t.Fatal(err)
	}
	if status.Installed {
		t.Fatal("expected Installed=false when no hook has ever been written")
	}
}
