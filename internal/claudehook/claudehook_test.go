package claudehook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSettingsPath(t *testing.T) {
	p := ProjectSettingsPath()
	if p != ".claude/settings.json" {
		t.Errorf("expected .claude/settings.json, got %s", p)
	}
}

func TestResolveSettingsPath(t *testing.T) {
	if ResolveSettingsPath("project") != ".claude/settings.json" {
		t.Error("expected project path")
	}
	if ResolveSettingsPath("user") == "" {
		t.Error("expected non-empty user path")
	}
}

func TestReadSettingsMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s, err := ReadSettings(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Hooks != nil {
		t.Error("expected nil hooks for missing file")
	}
}

func TestReadSettingsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte("not json"), 0644)
	_, err := ReadSettings(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("expected invalid JSON error, got: %v", err)
	}
}

func TestBackupSettingsCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte("{}"), 0644)
	backup, err := BackupSettings(path)
	if err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	if backup == "" {
		t.Fatal("expected backup path")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
}

func TestBackupSettingsMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	backup, err := BackupSettings(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backup != "" {
		t.Error("expected empty backup path for missing file")
	}
}

func TestHasXiTHookDetectsMarker(t *testing.T) {
	s := &Settings{
		Hooks: map[string][]HookEntry{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []HookDef{
						{Type: "command", Command: "/home/user/.xit/hooks/claude-pretooluse-bash.sh"},
					},
				},
			},
		},
	}
	if !HasXiTHook(s, "/home/user/.xit/hooks/claude-pretooluse-bash.sh") {
		t.Error("expected HasXiTHook true")
	}
}

func TestHasXiTHookMissing(t *testing.T) {
	s := &Settings{
		Hooks: map[string][]HookEntry{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []HookDef{
						{Type: "command", Command: "/other/hook.sh"},
					},
				},
			},
		},
	}
	if HasXiTHook(s, "/home/user/.xit/hooks/claude-pretooluse-bash.sh") {
		t.Error("expected HasXiTHook false")
	}
}

func TestAddXiTHookCreatesStructure(t *testing.T) {
	s := &Settings{}
	AddXiTHook(s, "/path/to/script.sh")
	entries, ok := s.Hooks["PreToolUse"]
	if !ok {
		t.Fatal("expected PreToolUse hook")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Matcher != "Bash" {
		t.Errorf("expected matcher Bash, got %s", entries[0].Matcher)
	}
	if len(entries[0].Hooks) != 1 {
		t.Fatalf("expected 1 hook def, got %d", len(entries[0].Hooks))
	}
	if entries[0].Hooks[0].Command != "/path/to/script.sh" {
		t.Errorf("expected command /path/to/script.sh, got %s", entries[0].Hooks[0].Command)
	}
}

func TestAddXiTHookDoesNotDuplicate(t *testing.T) {
	s := &Settings{}
	AddXiTHook(s, "/path/to/script.sh")
	AddXiTHook(s, "/path/to/script.sh")
	entries := s.Hooks["PreToolUse"]
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after re-add, got %d", len(entries))
	}
}

func TestRemoveXiTHook(t *testing.T) {
	s := &Settings{
		Hooks: map[string][]HookEntry{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []HookDef{
						{Type: "command", Command: "/other/hook.sh"},
						{Type: "command", Command: "/home/user/.xit/hooks/claude-pretooluse-bash.sh"},
					},
				},
			},
		},
	}
	removed := RemoveXiTHook(s, "/home/user/.xit/hooks/claude-pretooluse-bash.sh")
	if !removed {
		t.Error("expected RemoveXiTHook true")
	}
	entries := s.Hooks["PreToolUse"]
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry remaining, got %d", len(entries))
	}
	if len(entries[0].Hooks) != 1 {
		t.Fatalf("expected 1 hook remaining, got %d", len(entries[0].Hooks))
	}
	if entries[0].Hooks[0].Command != "/other/hook.sh" {
		t.Errorf("expected remaining /other/hook.sh, got %s", entries[0].Hooks[0].Command)
	}
}

func TestRemoveXiTHookDeletesEmptyPreToolUse(t *testing.T) {
	s := &Settings{
		Hooks: map[string][]HookEntry{
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks: []HookDef{
						{Type: "command", Command: "/home/user/.xit/hooks/claude-pretooluse-bash.sh"},
					},
				},
			},
		},
	}
	RemoveXiTHook(s, "/home/user/.xit/hooks/claude-pretooluse-bash.sh")
	if _, ok := s.Hooks["PreToolUse"]; ok {
		t.Error("expected PreToolUse deleted when empty")
	}
}

func TestInstallDryRun(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	home := t.TempDir()
	res, err := Install(settingsPath, home, true)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if res.SettingsPath != settingsPath {
		t.Errorf("expected settings path %s, got %s", settingsPath, res.SettingsPath)
	}
	_, err = os.Stat(settingsPath)
	if err == nil {
		t.Error("dry-run should not create settings")
	}
}

func TestInstallCreatesScriptAndSettings(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	home := t.TempDir()
	res, err := Install(settingsPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings not created: %v", err)
	}
	if !strings.Contains(string(data), "PreToolUse") {
		t.Error("expected PreToolUse in settings")
	}
	if !strings.Contains(string(data), "claude-pretooluse-bash.sh") {
		t.Error("expected hook script path in settings")
	}

	info, err := os.Stat(res.ScriptPath)
	if err != nil {
		t.Fatalf("script not created: %v", err)
	}
	if info.Mode().Perm()&0111 == 0 {
		t.Error("expected script executable")
	}
	scriptData, _ := os.ReadFile(res.ScriptPath)
	if !strings.Contains(string(scriptData), "XiT managed Claude Code hook") {
		t.Error("expected XiT marker in script")
	}
}

func TestInstallBacksUpExisting(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{"other":"value"}`), 0644)
	home := t.TempDir()
	res, err := Install(settingsPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.BackupPath == "" {
		t.Fatal("expected backup path")
	}
	backupData, _ := os.ReadFile(res.BackupPath)
	if !strings.Contains(string(backupData), `"other":"value"`) {
		t.Error("expected original content in backup")
	}
}

func TestInstallPreservesExistingHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/other/hook.sh"}]}]}}`), 0644)
	home := t.TempDir()
	_, err := Install(settingsPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	data, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(data), "/other/hook.sh") {
		t.Error("expected existing hook preserved")
	}
	if !strings.Contains(string(data), "claude-pretooluse-bash.sh") {
		t.Error("expected XiT hook added")
	}
}

func TestInstallUpdatesExistingXiTHook(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/old/.xit/hooks/claude-pretooluse-bash.sh"}]}]}}`), 0644)
	home := t.TempDir()
	_, err := Install(settingsPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	data, _ := os.ReadFile(settingsPath)
	if strings.Contains(string(data), "/old/") {
		t.Error("expected old XiT hook removed")
	}
	if !strings.Contains(string(data), home) {
		t.Error("expected new XiT hook path")
	}
}

func TestUninstallRemovesXiTHook(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/home/user/.xit/hooks/claude-pretooluse-bash.sh"}]}]}}`), 0644)
	home := t.TempDir()
	err := Uninstall(settingsPath, home, false)
	if err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	data, _ := os.ReadFile(settingsPath)
	if strings.Contains(string(data), "claude-pretooluse-bash.sh") {
		t.Error("expected XiT hook removed")
	}
}

func TestUninstallPreservesOtherHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/other/hook.sh"},{"type":"command","command":"/home/user/.xit/hooks/claude-pretooluse-bash.sh"}]}]}}`), 0644)
	home := t.TempDir()
	err := Uninstall(settingsPath, home, false)
	if err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	data, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(data), "/other/hook.sh") {
		t.Error("expected other hook preserved")
	}
}

func TestUninstallNotFound(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	home := t.TempDir()
	err := Uninstall(settingsPath, home, false)
	if err == nil {
		t.Fatal("expected error when hook not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestStatusInstalled(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	os.WriteFile(settingsPath, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/home/user/.xit/hooks/claude-pretooluse-bash.sh"}]}]}}`), 0644)
	home := t.TempDir()
	status, err := Status(settingsPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.Installed {
		t.Error("expected installed")
	}
	if status.Mode != "observe" {
		t.Errorf("expected observe mode, got %s", status.Mode)
	}
	if status.Rewrite {
		t.Error("expected rewrite false")
	}
	if !status.FailOpen {
		t.Error("expected fail_open true")
	}
}

func TestStatusNotInstalled(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	home := t.TempDir()
	status, err := Status(settingsPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.Installed {
		t.Error("expected not installed")
	}
}

func TestRecommendKnownCommands(t *testing.T) {
	cases := []string{
		"go test -v ./...",
		"npm test",
		"git diff",
		"docker logs",
	}
	for _, c := range cases {
		if recommend(c) == "" {
			t.Errorf("expected recommendation for %s", c)
		}
	}
}

func TestRecommendUnknownCommand(t *testing.T) {
	if recommend("echo hello") != "" {
		t.Error("expected no recommendation for echo hello")
	}
}

func TestDefaultHookConfig(t *testing.T) {
	cfg := DefaultHookConfig()
	if cfg.Mode != "observe" {
		t.Errorf("expected observe mode, got %s", cfg.Mode)
	}
	if !cfg.FailOpen {
		t.Error("expected fail_open true")
	}
}

func TestReadHookConfigMissing(t *testing.T) {
	home := t.TempDir()
	cfg, err := ReadHookConfig(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != "observe" {
		t.Errorf("expected observe, got %s", cfg.Mode)
	}
}

func TestWriteReadHookConfig(t *testing.T) {
	home := t.TempDir()
	cfg := &HookConfig{Mode: "reroute", FailOpen: true}
	if err := WriteHookConfig(home, cfg); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	loaded, err := ReadHookConfig(home)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if loaded.Mode != "reroute" {
		t.Errorf("expected reroute, got %s", loaded.Mode)
	}
}

func TestShouldRerouteGitDiff(t *testing.T) {
	ok, cmd := ShouldReroute("git diff")
	if !ok {
		t.Error("expected git diff to reroute")
	}
	if cmd != "xit auto git diff" {
		t.Errorf("expected xit auto git diff, got %s", cmd)
	}
}

func TestShouldRerouteGoTest(t *testing.T) {
	ok, cmd := ShouldReroute("go test -v ./...")
	if !ok {
		t.Error("expected go test to reroute")
	}
	if cmd != "xit auto go test -v ./..." {
		t.Errorf("expected xit auto go test -v ./..., got %s", cmd)
	}
}

func TestShouldNotRerouteGitStatus(t *testing.T) {
	ok, _ := ShouldReroute("git status")
	if ok {
		t.Error("expected git status not to reroute")
	}
}

func TestShouldNotRerouteNPMInstall(t *testing.T) {
	ok, _ := ShouldReroute("npm install")
	if ok {
		t.Error("expected npm install not to reroute")
	}
}

func TestShouldNotRerouteMachineReadable(t *testing.T) {
	ok, _ := ShouldReroute("git log --format=json")
	if ok {
		t.Error("expected machine-readable command not to reroute")
	}
}

func TestEnableDisableReroute(t *testing.T) {
	home := t.TempDir()
	if err := EnableReroute(home); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	cfg, _ := ReadHookConfig(home)
	if cfg.Mode != "reroute" {
		t.Errorf("expected reroute, got %s", cfg.Mode)
	}
	if err := DisableReroute(home); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	cfg, _ = ReadHookConfig(home)
	if cfg.Mode != "observe" {
		t.Errorf("expected observe, got %s", cfg.Mode)
	}
}

func TestStatsMissing(t *testing.T) {
	home := t.TempDir()
	stats, err := Stats(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.HasEvents {
		t.Error("expected no events")
	}
}

func TestStatsCounts(t *testing.T) {
	home := t.TempDir()
	logDir := filepath.Join(home, "claude-hooks")
	os.MkdirAll(logDir, 0755)
	lines := []string{
		`{"time":"2026-01-01T00:00:00Z","action":"reroute","original_command":"go test -v ./..."}`,
		`{"time":"2026-01-01T00:00:01Z","action":"reroute","original_command":"go test -v ./..."}`,
		`{"time":"2026-01-01T00:00:02Z","action":"passthrough","original_command":"git status"}`,
		`{"time":"2026-01-01T00:00:03Z","action":"error_fail_open"}`,
	}
	os.WriteFile(filepath.Join(logDir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
	stats, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.Events != 4 {
		t.Errorf("expected 4 events, got %d", stats.Events)
	}
	if stats.Rerouted != 2 {
		t.Errorf("expected 2 rerouted, got %d", stats.Rerouted)
	}
	if stats.Passthrough != 1 {
		t.Errorf("expected 1 passthrough, got %d", stats.Passthrough)
	}
	if stats.Errors != 1 {
		t.Errorf("expected 1 error, got %d", stats.Errors)
	}
	if len(stats.TopCommands) != 1 {
		t.Fatalf("expected 1 top command, got %d", len(stats.TopCommands))
	}
	if stats.TopCommands[0].Command != "go test -v ./..." || stats.TopCommands[0].Count != 2 {
		t.Errorf("unexpected top command: %+v", stats.TopCommands[0])
	}
}
