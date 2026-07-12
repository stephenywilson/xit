package codexhook

import "testing"

// TestDetectSurface locks in the ambient-signal priority: VSCODE_PID beats
// __CFBundleIdentifier, a ChatGPT.app bundle id maps to the desktop surface,
// and a plain terminal (neither signal) safely falls back to codex_cli —
// never guessing chatgpt_desktop_codex without real evidence.
func TestDetectSurface(t *testing.T) {
	cases := []struct {
		name        string
		vscodePID   string
		bundleID    string
		wantSurface string
	}{
		{"plain terminal, no signals", "", "", SurfaceCLI},
		{"plain terminal under Terminal.app", "", "com.apple.Terminal", SurfaceCLI},
		{"VS Code integrated terminal", "12345", "com.microsoft.VSCode", SurfaceIDE},
		{"VS Code present even without a bundle id", "12345", "", SurfaceIDE},
		{"ChatGPT desktop app's embedded Codex agent", "", "com.openai.codex", SurfaceChatGPTDesktop},
		{"VSCODE_PID takes priority over ChatGPT bundle id", "12345", "com.openai.codex", SurfaceIDE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VSCODE_PID", tc.vscodePID)
			t.Setenv("__CFBundleIdentifier", tc.bundleID)
			if got := DetectSurface(); got != tc.wantSurface {
				t.Fatalf("DetectSurface() = %q, want %q", got, tc.wantSurface)
			}
		})
	}
}

// TestSurfaceConstantsAreDistinctAndCodexScoped guards against silently
// reusing an existing generic surface value (cli/hook/vscode/bridge) for one
// of these codex-specific ones, which would collide with the Dashboard's
// generic "By Surface" buckets.
func TestSurfaceConstantsAreDistinctAndCodexScoped(t *testing.T) {
	values := []string{SurfaceCLI, SurfaceIDE, SurfaceChatGPTDesktop, SurfaceShared}
	seen := map[string]bool{}
	generic := map[string]bool{"cli": true, "hook": true, "vscode": true, "bridge": true}
	for _, v := range values {
		if seen[v] {
			t.Fatalf("duplicate surface constant value: %q", v)
		}
		seen[v] = true
		if generic[v] {
			t.Fatalf("surface constant %q collides with a generic (non-codex) surface value", v)
		}
	}
}
