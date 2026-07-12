package claudehook

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/vscodebridge"
)

// TestMain redirects vscodebridge's AppendEvent mirror write (MirrorHome())
// into a throwaway temp dir for this package's whole test run, so the many
// VS-Code-bridge tests below (which set VSCODE_PID) never append a real
// event into the machine's actual ~/.xit/events/vscode-ai-bridge.jsonl as a
// side effect of `go test`.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "xit-claudehook-test-mirror-home")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	os.Setenv("XIT_VSCODE_BRIDGE_HOME", tmp)
	os.Exit(m.Run())
}

// runHookCommand pipes payload into RunHookCommand's stdin and captures its
// stdout, mirroring internal/codexhook's test helper of the same shape.
func runHookCommand(t *testing.T, fallbackHome, payload string) string {
	t.Helper()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()
	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	if err := RunHookCommand(fallbackHome); err != nil {
		t.Fatalf("RunHookCommand failed: %v", err)
	}
	outW.Close()
	out, _ := io.ReadAll(outR)
	return string(out)
}

// runTurnHookCommand mirrors runHookCommand but for the turn-level handlers
// (HandleUserPromptSubmit / HandleStop), which share RunHookCommand's exact
// `func(fallbackHome string) error` shape but are exercised separately.
func runTurnHookCommand(t *testing.T, fallbackHome, payload string, handler func(string) error) string {
	t.Helper()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()
	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	if err := handler(fallbackHome); err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	outW.Close()
	out, _ := io.ReadAll(outR)
	return string(out)
}

func claudeBridgeEventTypes(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e vscodebridge.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid bridge event JSON: %v\n%s", err, line)
		}
		types = append(types, e.Event)
	}
	return types
}

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

// TestShouldRerouteFindHighDepth: `find . -maxdepth 4 -type f` is exactly
// the real-world command from the VS Code Claude Code panel screenshot that
// exposed this bug — it must classify as high-output.
func TestShouldRerouteFindHighDepth(t *testing.T) {
	ok, cmd := ShouldReroute("find . -maxdepth 4 -type f")
	if !ok {
		t.Fatal("expected find to reroute")
	}
	if cmd != "xit auto find . -maxdepth 4 -type f" {
		t.Errorf("expected xit auto find . -maxdepth 4 -type f, got %s", cmd)
	}
}

// TestShouldNotRerouteLowOutputCommands guards against over-eager
// classification flagging short, low-noise commands.
func TestShouldNotRerouteLowOutputCommands(t *testing.T) {
	for _, cmd := range []string{"pwd", "whoami", "echo hello", "git status --short"} {
		if ok, _ := ShouldReroute(cmd); ok {
			t.Errorf("expected %q not to reroute", cmd)
		}
	}
}

// TestIsAlreadyWrappedPreventsDoubleWrap: once a command is already
// "xit auto ...", ShouldReroute must never wrap it again (it sees tool="xit",
// not "find"/"go"/etc.), and isAlreadyWrapped must recognize it directly.
func TestIsAlreadyWrappedPreventsDoubleWrap(t *testing.T) {
	cmd := "xit auto find . -maxdepth 4 -type f"
	if !isAlreadyWrapped(cmd) {
		t.Fatal("expected isAlreadyWrapped to recognize an already-wrapped command")
	}
	if ok, _ := ShouldReroute(cmd); ok {
		t.Errorf("expected an already-wrapped command not to be rerouted again, got reroute for: %s", cmd)
	}
	if !isAlreadyWrapped("./xit auto go test -v ./...") {
		t.Fatal("expected isAlreadyWrapped to recognize the ./xit auto form too")
	}
	if isAlreadyWrapped("go test -v ./...") {
		t.Fatal("expected isAlreadyWrapped to be false for an unwrapped command")
	}
}

// TestResolveClaudeHomePrefersPayloadCwd is the regression test for the
// real bug this task found: RunHookCommand previously always used the
// caller-supplied fallback (~/.xit), completely ignoring which project
// Claude Code was actually working in. That meant a project-local
// .xit/claude-hooks/config.json with "mode": "reroute" was silently never
// read — only the global ~/.xit/claude-hooks/config.json (mode: "observe")
// ever applied, for every project, including from inside the VS Code Claude
// Code panel.
func TestResolveClaudeHomePrefersPayloadCwd(t *testing.T) {
	fallback := "/Users/someone/.xit"
	projectCwd := "/Users/someone/projects/xit"
	got := resolveClaudeHome(fallback, projectCwd)
	want := filepath.Join(projectCwd, ".xit")
	if got != want {
		t.Fatalf("resolveClaudeHome(%q, %q) = %q, want %q", fallback, projectCwd, got, want)
	}
}

func TestResolveClaudeHomeFallsBackWhenCwdEmpty(t *testing.T) {
	fallback := "/Users/someone/.xit"
	if got := resolveClaudeHome(fallback, ""); got != fallback {
		t.Fatalf("resolveClaudeHome(%q, \"\") = %q, want fallback %q", fallback, got, fallback)
	}
}

func TestResolveClaudeHomeXITHomeEnvWins(t *testing.T) {
	t.Setenv("XIT_HOME", "/explicit/xit/home")
	if got := resolveClaudeHome("/fallback/.xit", "/some/project"); got != "/explicit/xit/home" {
		t.Fatalf("expected explicit XIT_HOME to win, got %q", got)
	}
}

// TestRunHookCommandUsesProjectLocalConfigViaPayloadCwd is the end-to-end
// regression test for the home-resolution bug: a project-local
// .xit/claude-hooks/config.json with "mode": "reroute" must actually be
// read (and acted on) when the hook payload's cwd points at that project —
// even though fallbackHome (simulating the global ~/.xit) has "observe".
func TestRunHookCommandUsesProjectLocalConfigViaPayloadCwd(t *testing.T) {
	// fail_open=true only denies when xit auto is confirmed available; stub
	// that explicitly (this test is about home resolution, not availability).
	withFakeAvailability(t, xitAvailability{Available: true, Reason: "available"})
	globalHome := filepath.Join(t.TempDir(), "global-xit")
	if err := WriteHookConfig(globalHome, &HookConfig{Mode: "observe", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")
	if err := WriteHookConfig(projectHome, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}

	payload := `{"session_id":"s1","cwd":"` + projectDir + `","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, globalHome, payload)

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	hso, _ := resp["hookSpecificOutput"].(map[string]interface{})
	if hso == nil || hso["permissionDecision"] != "deny" {
		t.Fatalf("expected a deny decision (project-local reroute mode), got: %s", out)
	}
	reason, _ := hso["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "xit auto find . -maxdepth 4 -type f") {
		t.Errorf("expected recommended command in reason, got: %q", reason)
	}

	// Confirm it logged to the PROJECT-local events file, not the global one.
	if _, err := os.Stat(filepath.Join(projectHome, "claude-hooks", "events.jsonl")); err != nil {
		t.Fatalf("expected project-local events.jsonl to exist: %v", err)
	}
}

// TestRunHookCommandStartsVSCodeBridgeForAlreadyWrappedCommand: Claude
// Code's PreToolUse hook protocol can't mutate tool_input like Codex's can
// (see isAlreadyWrapped's doc comment), so XiT can only start tracking a VS
// Code bridge run once the command is ALREADY "xit auto ..." — either the AI
// wrote it directly (CLAUDE.md rules) or it's the retry after a reroute
// recommendation.
func TestRunHookCommandStartsVSCodeBridgeForAlreadyWrappedCommand(t *testing.T) {
	t.Setenv("VSCODE_PID", "4242")
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")

	payload := `{"session_id":"s1","cwd":"` + projectDir + `","tool_name":"Bash","tool_input":{"command":"xit auto find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload)

	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) for an already-wrapped command, got: %s", out)
	}

	data, err := os.ReadFile(filepath.Join(projectHome, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatalf("expected a VS Code bridge run.started event: %v", err)
	}
	var event vscodebridge.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("invalid bridge event JSON: %v\n%s", err, data)
	}
	if event.Event != "run.started" || event.Adapter != vscodebridge.AdapterClaude || event.Surface != vscodebridge.SurfaceClaudeCode {
		t.Fatalf("bad bridge started event: %+v", event)
	}
	if strings.Contains(string(data), "find . -maxdepth 4") || strings.Contains(string(data), projectDir) {
		t.Fatalf("bridge event leaked raw command/cwd: %s", data)
	}
}

// TestRunHookCommandDoesNotStartVSCodeBridgeOutsideVSCode confirms ordinary
// Claude CLI usage (no VSCODE_PID) never starts a bridge run, even for an
// already-wrapped command.
func TestRunHookCommandDoesNotStartVSCodeBridgeOutsideVSCode(t *testing.T) {
	t.Setenv("VSCODE_PID", "")
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")

	payload := `{"session_id":"s1","cwd":"` + projectDir + `","tool_name":"Bash","tool_input":{"command":"xit auto go test -v ./..."}}`
	_ = runHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload)

	if _, err := os.Stat(filepath.Join(projectHome, "events", "vscode-ai-bridge.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected no bridge file for ordinary Claude CLI usage, err=%v", err)
	}
}

// ──────────────────────────────────────────────────────────────────
// HandleUserPromptSubmit / HandleStop: turn-LEVEL VS Code Bridge signals.
// NOT installed by default (see install.go / docs/claude.md) — whether the
// real VS Code Claude Code panel reliably triggers UserPromptSubmit/Stop is
// unconfirmed. These tests cover the capability itself so it is ready to
// enable once that's verified, without claiming it is wired into the
// default `.claude/settings.json` install.
// ──────────────────────────────────────────────────────────────────

func TestHandleUserPromptSubmitStartsVSCodeBridgeTurn(t *testing.T) {
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")
	t.Setenv("VSCODE_PID", "4242")

	payload := `{"session_id":"s1","cwd":"` + projectDir + `","prompt":"please run the tests"}`
	out := runTurnHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload, HandleUserPromptSubmit)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected {} (allow), got: %q", out)
	}
	types := claudeBridgeEventTypes(t, projectHome)
	if len(types) != 1 || types[0] != "turn.started" {
		t.Fatalf("expected exactly one turn.started bridge event, got: %v", types)
	}
}

func TestHandleUserPromptSubmitNoBridgeOutsideVSCode(t *testing.T) {
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")
	t.Setenv("VSCODE_PID", "")

	payload := `{"session_id":"s1","cwd":"` + projectDir + `","prompt":"please run the tests"}`
	runTurnHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload, HandleUserPromptSubmit)
	if types := claudeBridgeEventTypes(t, projectHome); types != nil {
		t.Fatalf("expected no bridge event outside VS Code, got: %v", types)
	}
}

func TestHandleUserPromptSubmitNoSessionIDIsNoop(t *testing.T) {
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")
	t.Setenv("VSCODE_PID", "4242")

	payload := `{"cwd":"` + projectDir + `","prompt":"please run the tests"}`
	runTurnHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload, HandleUserPromptSubmit)
	if types := claudeBridgeEventTypes(t, projectHome); types != nil {
		t.Fatalf("expected no bridge event without a session_id, got: %v", types)
	}
}

func TestHandleStopFinishesVSCodeBridgeTurn(t *testing.T) {
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")
	t.Setenv("VSCODE_PID", "4242")

	payload := `{"session_id":"s1","cwd":"` + projectDir + `"}`
	out := runTurnHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload, HandleStop)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected {} (allow), got: %q", out)
	}
	types := claudeBridgeEventTypes(t, projectHome)
	if len(types) != 1 || types[0] != "turn.finished" {
		t.Fatalf("expected exactly one turn.finished bridge event, got: %v", types)
	}
}

func TestHandleStopNoBridgeOutsideVSCode(t *testing.T) {
	projectDir := t.TempDir()
	projectHome := filepath.Join(projectDir, ".xit")
	t.Setenv("VSCODE_PID", "")

	payload := `{"session_id":"s1","cwd":"` + projectDir + `"}`
	runTurnHookCommand(t, filepath.Join(t.TempDir(), "global-xit"), payload, HandleStop)
	if types := claudeBridgeEventTypes(t, projectHome); types != nil {
		t.Fatalf("expected no bridge event outside VS Code, got: %v", types)
	}
}

// TestPreToolUseRerouteDenyUnaffectedByTurnHooks: the existing deny +
// recommended-command behavior (PreToolUse, "reroute" mode) must not
// regress now that HandleUserPromptSubmit/HandleStop also exist.
func TestPreToolUseRerouteDenyUnaffectedByTurnHooks(t *testing.T) {
	// fail_open=true only denies when xit auto is confirmed available; stub
	// that explicitly so this test doesn't depend on whether this machine's
	// PATH/version-check cache happens to report xit auto as usable.
	withFakeAvailability(t, xitAvailability{Available: true, Reason: "available"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	// No "cwd" field in the payload, so resolveClaudeHome falls back to the
	// fallbackHome passed below (= home) — same as the existing reroute
	// tests in cmd/xit/main_test.go.
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, home, payload)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	hso, _ := resp["hookSpecificOutput"].(map[string]interface{})
	if hso == nil || hso["permissionDecision"] != "deny" {
		t.Fatalf("expected deny decision unaffected by the new turn hooks, got: %s", out)
	}
	reason, _ := hso["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "xit auto find . -maxdepth 4 -type f") {
		t.Errorf("expected recommended command in reason, got: %q", reason)
	}
}
