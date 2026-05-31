package cursorhook

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadHooksConfigMissing(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "hooks.json")
	cfg, err := ReadHooksConfig(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if len(cfg.Hooks) != 0 {
		t.Errorf("expected empty hooks, got %v", cfg.Hooks)
	}
}

func TestHasXiTHook(t *testing.T) {
	cfg := &HooksConfig{
		Hooks: map[string][]HookEntry{
			"beforeShellExecution": {
				{Command: "/some/other/hook"},
				{Command: "/home/user/.xit/hooks/cursor-before-shell-exec.sh"},
			},
		},
		Version: 1,
	}
	if !HasXiTHook(cfg) {
		t.Error("expected HasXiTHook true")
	}
	cfg2 := &HooksConfig{
		Hooks: map[string][]HookEntry{
			"beforeShellExecution": {
				{Command: "/some/other/hook"},
			},
		},
		Version: 1,
	}
	if HasXiTHook(cfg2) {
		t.Error("expected HasXiTHook false")
	}
}

func TestAddRemoveXiTHook(t *testing.T) {
	cfg := &HooksConfig{
		Hooks: map[string][]HookEntry{
			"beforeShellExecution": {
				{Command: "/other/hook"},
			},
		},
		Version: 1,
	}
	AddXiTHook(cfg, "/home/user/.xit/hooks/cursor-before-shell-exec.sh")
	if len(cfg.Hooks["beforeShellExecution"]) != 2 {
		t.Errorf("expected 2 entries, got %d", len(cfg.Hooks["beforeShellExecution"]))
	}
	RemoveXiTHook(cfg)
	if len(cfg.Hooks["beforeShellExecution"]) != 1 {
		t.Errorf("expected 1 entry after remove, got %d", len(cfg.Hooks["beforeShellExecution"]))
	}
	if cfg.Hooks["beforeShellExecution"][0].Command != "/other/hook" {
		t.Errorf("expected /other/hook, got %s", cfg.Hooks["beforeShellExecution"][0].Command)
	}
}

func TestInstallDryRun(t *testing.T) {
	tmp := t.TempDir()
	hooksPath := filepath.Join(tmp, "hooks.json")
	home := filepath.Join(tmp, ".xit")
	res, err := Install(hooksPath, home, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AlreadyInstalled {
		t.Error("expected not already installed")
	}
	if res.HooksPath != hooksPath {
		t.Errorf("expected hooksPath %s, got %s", hooksPath, res.HooksPath)
	}
}

func TestInstallAndUninstall(t *testing.T) {
	tmp := t.TempDir()
	hooksPath := filepath.Join(tmp, "hooks.json")
	home := filepath.Join(tmp, ".xit")

	res, err := Install(hooksPath, home, false)
	if err != nil {
		t.Fatalf("install error: %v", err)
	}
	if res.HooksPath != hooksPath {
		t.Errorf("unexpected hooksPath: %s", res.HooksPath)
	}

	cfg, err := ReadHooksConfig(hooksPath)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if !HasXiTHook(cfg) {
		t.Error("expected XiT hook installed")
	}

	if err := Uninstall(hooksPath, home); err != nil {
		t.Fatalf("uninstall error: %v", err)
	}
	cfg2, err := ReadHooksConfig(hooksPath)
	if err != nil {
		t.Fatalf("read error after uninstall: %v", err)
	}
	if HasXiTHook(cfg2) {
		t.Error("expected XiT hook removed")
	}
}

func TestStatus(t *testing.T) {
	tmp := t.TempDir()
	hooksPath := filepath.Join(tmp, "hooks.json")
	home := filepath.Join(tmp, ".xit")

	st, err := Status(hooksPath, home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Installed {
		t.Error("expected not installed")
	}
	if st.Mode != "observe" {
		t.Errorf("expected observe mode, got %s", st.Mode)
	}
}

func TestStatsEmpty(t *testing.T) {
	tmp := t.TempDir()
	stats, err := Stats(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Events != 0 {
		t.Errorf("expected 0 events, got %d", stats.Events)
	}
}

func TestStatsWithEvents(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "cursor-hooks"), 0755)
	data := `{"time":"2026-01-01T00:00:00Z","action":"observe"}
{"time":"2026-01-01T00:00:01Z","action":"passthrough"}
{"time":"2026-01-01T00:00:02Z","action":"fail_open"}
`
	os.WriteFile(filepath.Join(tmp, "cursor-hooks", "events.jsonl"), []byte(data), 0644)
	stats, err := Stats(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Events != 3 {
		t.Errorf("expected 3 events, got %d", stats.Events)
	}
	if stats.Observed != 1 {
		t.Errorf("expected 1 observed, got %d", stats.Observed)
	}
	if stats.Passthrough != 1 {
		t.Errorf("expected 1 passthrough, got %d", stats.Passthrough)
	}
	if stats.Errors != 1 {
		t.Errorf("expected 1 error, got %d", stats.Errors)
	}
}

func TestRunHookCommand(t *testing.T) {
	tmp := t.TempDir()
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(`{"command":"go test -v ./...","cwd":"/tmp/test","sandbox":false}`)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(tmp)
	outW.Close()

	outBytes, _ := io.ReadAll(outR)
	if !strings.Contains(string(outBytes), `"permission"`) {
		t.Errorf("expected permission in output, got: %s", string(outBytes))
	}

	eventsPath := filepath.Join(tmp, "cursor-hooks", "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("events file missing: %v", err)
	}
	if !strings.Contains(string(data), `"adapter":"cursor"`) {
		t.Errorf("expected adapter cursor in events, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"policy":"should_compress"`) {
		t.Errorf("expected should_compress policy, got: %s", string(data))
	}
}
