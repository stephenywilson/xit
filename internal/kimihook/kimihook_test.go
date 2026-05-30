package kimihook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectConfigPath(t *testing.T) {
	p := ProjectConfigPath()
	if p != ".kimi/config.toml" {
		t.Errorf("expected .kimi/config.toml, got %s", p)
	}
}

func TestUserConfigPath(t *testing.T) {
	p := UserConfigPath()
	if !strings.Contains(p, ".kimi/config.toml") {
		t.Errorf("expected path containing .kimi/config.toml, got %s", p)
	}
}

func TestResolveConfigPathProject(t *testing.T) {
	p := ResolveConfigPath("project")
	if p != ".kimi/config.toml" {
		t.Errorf("expected project path, got %s", p)
	}
}

func TestResolveConfigPathUser(t *testing.T) {
	p := ResolveConfigPath("user")
	if p == ".kimi/config.toml" || !strings.Contains(p, ".kimi/config.toml") {
		t.Errorf("expected user path, got %s", p)
	}
}

func TestDetectConfigFormatTOML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[[hooks]]\n"), 0644)
	if DetectConfigFormat(p) != FormatTOML {
		t.Errorf("expected toml format")
	}
}

func TestDetectConfigFormatJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte("{}"), 0644)
	if DetectConfigFormat(p) != FormatJSON {
		t.Errorf("expected json format")
	}
}

func TestDetectConfigFormatMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.toml")
	if DetectConfigFormat(p) != FormatNone {
		t.Errorf("expected none format for missing file")
	}
}

func TestReadTomlMissingReturnsEmpty(t *testing.T) {
	content, err := ReadToml(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty content for missing file")
	}
}

func TestWriteTomlRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	original := "[provider]\nmodel = \"kimi\"\n"
	if err := WriteToml(p, original); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	loaded, err := ReadToml(p)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if loaded != original {
		t.Errorf("expected %q, got %q", original, loaded)
	}
}

func TestHasXiTHookToml(t *testing.T) {
	content := "[[hooks]]\nevent = \"PreToolUse\"\ncommand = \"/home/user/.xit/hooks/kimi-pretooluse-shell.sh\"\n"
	if !HasXiTHookToml(content) {
		t.Error("expected XiT hook detected in TOML")
	}
}

func TestHasXiTHookTomlMissing(t *testing.T) {
	content := "[[hooks]]\nevent = \"PreToolUse\"\ncommand = \"echo other\"\n"
	if HasXiTHookToml(content) {
		t.Error("expected no XiT hook")
	}
}

func TestRemoveXiTHookToml(t *testing.T) {
	content := `[provider]
model = "kimi"

[[hooks]]
event = "PreToolUse"
matcher = "Shell"
command = "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"

[[hooks]]
event = "PreToolUse"
matcher = "Bash"
command = "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"

[[hooks]]
event = "PostToolUse"
command = "echo other"
`
	result := RemoveXiTHookToml(content)
	if strings.Contains(result, "kimi-pretooluse-shell.sh") {
		t.Error("expected XiT hooks removed")
	}
	if !strings.Contains(result, "echo other") {
		t.Error("expected non-XiT hook preserved")
	}
	if !strings.Contains(result, "model = \"kimi\"") {
		t.Error("expected provider config preserved")
	}
}

func TestAddXiTHookTomlCreatesBlocks(t *testing.T) {
	scriptPath := "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"
	result, err := AddXiTHookToml("", scriptPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[[hooks]]") {
		t.Error("expected hooks block")
	}
	if !strings.Contains(result, "matcher = \"Shell\"") {
		t.Error("expected Shell matcher")
	}
	if !strings.Contains(result, "matcher = \"Bash\"") {
		t.Error("expected Bash matcher")
	}
	if !strings.Contains(result, scriptPath) {
		t.Error("expected script path")
	}
}

func TestAddXiTHookTomlAppendsToExisting(t *testing.T) {
	existing := "[provider]\nmodel = \"kimi\"\n"
	scriptPath := "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"
	result, err := AddXiTHookToml(existing, scriptPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[provider]") {
		t.Error("expected existing content preserved")
	}
	if !strings.Contains(result, "[[hooks]]") {
		t.Error("expected hooks appended")
	}
}

func TestAddXiTHookTomlIdempotent(t *testing.T) {
	scriptPath := "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"
	first, err := AddXiTHookToml("", scriptPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count hooks blocks (2 PreToolUse only when turnScripts is nil)
	count := strings.Count(first, "[[hooks]]")
	if count != 2 {
		t.Fatalf("expected 2 hooks blocks, got %d", count)
	}
	second, err := AddXiTHookToml(first, scriptPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	count2 := strings.Count(second, "[[hooks]]")
	if count2 != 2 {
		t.Fatalf("expected still 2 hooks blocks after re-add, got %d", count2)
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	res, err := Install(configPath, home, true)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if res.ConfigPath != configPath {
		t.Errorf("expected configPath %s, got %s", configPath, res.ConfigPath)
	}
	if res.Format != FormatTOML {
		t.Errorf("expected toml format for new file, got %s", res.Format)
	}
	_, err = os.Stat(configPath)
	if err == nil {
		t.Error("dry-run should not create config")
	}
}

func TestInstallCreatesTOMLConfig(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	res, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.Format != FormatTOML {
		t.Errorf("expected toml format, got %s", res.Format)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[[hooks]]") {
		t.Errorf("expected TOML hooks block, got:\n%s", content)
	}
	if !strings.Contains(content, "matcher = \"Shell\"") {
		t.Errorf("expected Shell matcher, got:\n%s", content)
	}
	if !strings.Contains(content, "matcher = \"Bash\"") {
		t.Errorf("expected Bash matcher, got:\n%s", content)
	}
	scriptData, err := os.ReadFile(res.ScriptPath)
	if err != nil {
		t.Fatalf("script not created: %v", err)
	}
	if !strings.Contains(string(scriptData), "kimi-hook observe") {
		t.Errorf("expected script to call kimi-hook observe, got:\n%s", string(scriptData))
	}
}

func TestInstallPreservesExistingTOML(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	existing := "[provider]\nmodel = \"kimi\"\n\n[mcp]\nenabled = true\n"
	os.WriteFile(configPath, []byte(existing), 0644)
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	content := string(data)
	if !strings.Contains(content, "[provider]") {
		t.Errorf("expected provider preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "[mcp]") {
		t.Errorf("expected mcp preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "[[hooks]]") {
		t.Errorf("expected hooks appended, got:\n%s", content)
	}
}

func TestInstallBacksUpExistingTOML(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	os.WriteFile(configPath, []byte("model = \"kimi\"\n"), 0644)
	home := filepath.Join(tmp, ".xit")
	res, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.BackupPath == "" {
		t.Error("expected backup path for existing config")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Errorf("backup file missing: %v", err)
	}
}

func TestInstallDoesNotDuplicate(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("first install failed: %v", err)
	}
	res, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("second install failed: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	count := strings.Count(string(data), "[[hooks]]")
	if count != 6 {
		t.Errorf("expected 6 hooks blocks after reinstall, got %d", count)
	}
	if !res.AlreadyInstalled {
		t.Error("expected AlreadyInstalled true on second install")
	}
}

func TestUninstallRemovesOnlyXiTHooks(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Add a non-XiT hook manually.
	data, _ := os.ReadFile(configPath)
	content := string(data) + "\n[[hooks]]\nevent = \"PostToolUse\"\ncommand = \"echo other\"\n"
	os.WriteFile(configPath, []byte(content), 0644)

	err = Uninstall(configPath, home, false)
	if err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	data, _ = os.ReadFile(configPath)
	result := string(data)
	if strings.Contains(result, "kimi-pretooluse-shell.sh") {
		t.Error("expected XiT hook removed")
	}
	if !strings.Contains(result, "echo other") {
		t.Error("expected non-XiT hook preserved")
	}
}

func TestUninstallNotFound(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	err := Uninstall(configPath, home, false)
	if err == nil {
		t.Error("expected error when XiT hook not found")
	}
}

func TestStatusInstalledTOML(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	status, err := Status(configPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.Installed {
		t.Error("expected installed")
	}
	if status.Format != FormatTOML {
		t.Errorf("expected toml format, got %s", status.Format)
	}
}

func TestStatusNotInstalled(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	home := filepath.Join(tmp, ".xit")
	status, err := Status(configPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.Installed {
		t.Error("expected not installed")
	}
}

func TestStatusLegacyJSON(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.json")
	home := filepath.Join(tmp, ".xit")
	scriptPath := filepath.Join(home, "hooks", "kimi-pretooluse-shell.sh")
	os.MkdirAll(filepath.Dir(scriptPath), 0755)
	os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0755)
	s := &Settings{Hooks: map[string][]HookEntry{
		"PreToolUse": {{Matcher: "Bash", Hooks: []HookDef{{Type: "command", Command: scriptPath}}}},
	}}
	WriteSettings(configPath, s)
	status, err := Status(configPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.Installed {
		t.Error("expected installed for legacy JSON")
	}
	if status.Format != FormatJSON {
		t.Errorf("expected json format, got %s", status.Format)
	}
}

func TestRunHookCommandFailOpenOnInvalidInput(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")

	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		w.WriteString("not json")
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("RunHookCommand should not error: %v", err)
	}
	outData := make([]byte, 1024)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {}, got %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("event log not created: %v", err)
	}
	if !strings.Contains(string(data), "error_fail_open") {
		t.Errorf("expected error_fail_open in log, got:\n%s", string(data))
	}
}

func TestRunHookCommandObservesBash(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")

	payload := `{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("RunHookCommand should not error: %v", err)
	}

	outData := make([]byte, 1024)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {}, got %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("event log not created: %v", err)
	}
	if !strings.Contains(string(data), "go test -v ./...") {
		t.Errorf("expected command in log, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "observe") {
		t.Errorf("expected observe action in log, got:\n%s", string(data))
	}
}

func TestRunHookCommandObservesShell(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")

	payload := `{"tool_name":"Shell","tool_input":{"command":"git status"}}`
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("RunHookCommand should not error: %v", err)
	}

	outData := make([]byte, 1024)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {}, got %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "git status") {
		t.Errorf("expected command in log, got:\n%s", string(data))
	}
}

func TestRunHookCommandPassthroughNonBash(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")

	payload := `{"tool_name":"Read","tool_input":{"file_path":"main.go"}}`
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("RunHookCommand should not error: %v", err)
	}

	outData := make([]byte, 1024)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {}, got %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "passthrough") {
		t.Errorf("expected passthrough in log, got:\n%s", string(data))
	}
}

func TestRunHookCommandNoDeny(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")

	payload := `{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	outData := make([]byte, 1024)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected empty allow response {}, got %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, _ := os.ReadFile(logPath)
	if strings.Contains(string(data), "deny") {
		t.Errorf("observe mode must not contain deny, got:\n%s", string(data))
	}
}

func TestAddXiTHookTomlRemovesEmptyHooksArray(t *testing.T) {
	scriptPath := "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"
	existing := "default_model = \"kimi\"\nhooks = []\n\n[provider]\nmodel = \"kimi\"\n"
	result, err := AddXiTHookToml(existing, scriptPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "hooks = []") {
		t.Errorf("expected empty hooks array removed, got:\n%s", result)
	}
	if !strings.Contains(result, "default_model") {
		t.Error("expected default_model preserved")
	}
	if !strings.Contains(result, "[provider]") {
		t.Error("expected provider section preserved")
	}
	if !strings.Contains(result, "[[hooks]]") {
		t.Error("expected hooks blocks added")
	}
}

func TestAddXiTHookTomlRemovesSpacedEmptyHooksArray(t *testing.T) {
	scriptPath := "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"
	existing := "hooks=[]\n"
	result, err := AddXiTHookToml(existing, scriptPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "hooks=") {
		t.Errorf("expected empty hooks array removed, got:\n%s", result)
	}
}

func TestAddXiTHookTomlRejectsNonEmptyInlineHooks(t *testing.T) {
	scriptPath := "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"
	existing := "hooks = [{ event = \"PreToolUse\" }]\n"
	_, err := AddXiTHookToml(existing, scriptPath, nil)
	if err == nil {
		t.Fatal("expected error for non-empty inline hooks array")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected unsupported error, got: %v", err)
	}
}

func TestHasHooksConflictToml(t *testing.T) {
	content := "hooks = []\n\n[[hooks]]\nevent = \"PreToolUse\"\n"
	if !HasHooksConflictToml(content) {
		t.Error("expected conflict detected")
	}
}

func TestHasHooksConflictTomlNoConflict(t *testing.T) {
	content := "[[hooks]]\nevent = \"PreToolUse\"\n"
	if HasHooksConflictToml(content) {
		t.Error("expected no conflict")
	}
}

func TestInstallOnTomlWithEmptyHooksArray(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	existing := "default_model = \"kimi\"\nhooks = []\n\n[provider]\nmodel = \"kimi\"\n"
	os.WriteFile(configPath, []byte(existing), 0644)
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	content := string(data)
	if strings.Contains(content, "hooks = []") {
		t.Errorf("expected empty hooks array removed, got:\n%s", content)
	}
	if !strings.Contains(content, "default_model") {
		t.Error("expected default_model preserved")
	}
	if !strings.Contains(content, "[provider]") {
		t.Error("expected provider preserved")
	}
	if !strings.Contains(content, "[[hooks]]") {
		t.Error("expected hooks blocks added")
	}
}

func TestInstallRefusesNonEmptyInlineHooks(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	existing := "hooks = [{ event = \"PreToolUse\" }]\n"
	os.WriteFile(configPath, []byte(existing), 0644)
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err == nil {
		t.Fatal("expected install to fail for non-empty inline hooks")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected unsupported error, got: %v", err)
	}
}

func TestStatusDetectsConflict(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	content := "hooks = []\n\n[[hooks]]\nevent = \"PreToolUse\"\nmatcher = \"Shell\"\ncommand = \"/home/user/.xit/hooks/kimi-pretooluse-shell.sh\"\n"
	os.WriteFile(configPath, []byte(content), 0644)
	home := filepath.Join(tmp, ".xit")
	status, err := Status(configPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.HasConflict {
		t.Error("expected conflict detected")
	}
}

func TestStatusNoConflictAfterFix(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	os.WriteFile(configPath, []byte("hooks = []\n"), 0644)
	home := filepath.Join(tmp, ".xit")
	_, err := Install(configPath, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	status, err := Status(configPath, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if status.HasConflict {
		t.Error("expected no conflict after install fix")
	}
}

func TestUninstallPreservesNonXiTHooks(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "config.toml")
	content := `[[hooks]]
event = "PreToolUse"
matcher = "Shell"
command = "/home/user/.xit/hooks/kimi-pretooluse-shell.sh"

[[hooks]]
event = "PostToolUse"
command = "echo other"
`
	os.WriteFile(configPath, []byte(content), 0644)
	home := filepath.Join(tmp, ".xit")
	if err := Uninstall(configPath, home, false); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	data, _ := os.ReadFile(configPath)
	result := string(data)
	if strings.Contains(result, "kimi-pretooluse-shell.sh") {
		t.Error("expected XiT hook removed")
	}
	if !strings.Contains(result, "echo other") {
		t.Error("expected non-XiT hook preserved")
	}
}

func TestDefaultHookConfig(t *testing.T) {
	cfg := DefaultHookConfig()
	if cfg.Mode != "observe" {
		t.Errorf("expected observe mode, got %s", cfg.Mode)
	}
	if cfg.RerouteEnabled {
		t.Error("expected reroute disabled by default")
	}
	if !cfg.FailOpen {
		t.Error("expected fail_open true by default")
	}
	if !cfg.InlineStatus {
		t.Error("expected inline_status true by default")
	}
	if cfg.StatusStyle != "compact" {
		t.Errorf("expected compact status_style by default, got %s", cfg.StatusStyle)
	}
}

func TestReadWriteHookConfig(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	cfg := DefaultHookConfig()
	if err := WriteHookConfig(home, cfg); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	loaded, err := ReadHookConfig(home)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if loaded.Mode != "observe" {
		t.Errorf("expected observe, got %s", loaded.Mode)
	}
}

func TestEnableDisableReroute(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	if err := EnableReroute(home); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	cfg, err := ReadHookConfig(home)
	if err != nil {
		t.Fatalf("read after enable failed: %v", err)
	}
	if cfg.Mode != "reroute" {
		t.Errorf("expected reroute mode, got %s", cfg.Mode)
	}
	if !cfg.RerouteEnabled {
		t.Error("expected reroute enabled")
	}
	if err := DisableReroute(home); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	cfg, err = ReadHookConfig(home)
	if err != nil {
		t.Fatalf("read after disable failed: %v", err)
	}
	if cfg.Mode != "observe" {
		t.Errorf("expected observe mode, got %s", cfg.Mode)
	}
	if cfg.RerouteEnabled {
		t.Error("expected reroute disabled")
	}
}

func TestShouldRerouteGitDiff(t *testing.T) {
	ok, rec := ShouldReroute("git diff")
	if !ok {
		t.Error("expected git diff to reroute")
	}
	if rec != "xit auto git diff" {
		t.Errorf("expected xit auto git diff, got %s", rec)
	}
}

func TestShouldRerouteGoTest(t *testing.T) {
	ok, rec := ShouldReroute("go test -v ./...")
	if !ok {
		t.Error("expected go test to reroute")
	}
	if rec != "xit auto go test -v ./..." {
		t.Errorf("expected xit auto go test -v ./..., got %s", rec)
	}
}

func TestShouldPassthroughGitStatus(t *testing.T) {
	ok, _ := ShouldReroute("git status")
	if ok {
		t.Error("expected git status to passthrough")
	}
}

func TestShouldPassthroughNpmInstall(t *testing.T) {
	ok, _ := ShouldReroute("npm install")
	if ok {
		t.Error("expected npm install to passthrough")
	}
}

func TestShouldPassthroughMachineReadable(t *testing.T) {
	ok, _ := ShouldReroute("git status --porcelain")
	if ok {
		t.Error("expected --porcelain to passthrough")
	}
	ok, _ = ShouldReroute("some-tool --json")
	if ok {
		t.Error("expected --json to passthrough")
	}
}

func TestRunHookCommandRerouteDeny(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")
	if err := EnableReroute(home); err != nil {
		t.Fatalf("enable failed: %v", err)
	}

	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("RunHookCommand should not error: %v", err)
	}

	outData := make([]byte, 2048)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if !strings.Contains(out, "permissionDecision") {
		t.Errorf("expected deny response, got: %s", out)
	}
	if !strings.Contains(out, "xit auto go test -v ./...") {
		t.Errorf("expected recommended command in deny reason, got: %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "reroute") {
		t.Errorf("expected reroute action in log, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), `"mode":"reroute"`) {
		t.Errorf("expected mode=reroute in log, got:\n%s", string(data))
	}
}

func TestRunHookCommandReroutePassthrough(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")
	if err := EnableReroute(home); err != nil {
		t.Fatalf("enable failed: %v", err)
	}

	payload := `{"tool_name":"Shell","tool_input":{"command":"git status"}}`
	r, w, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	oldStdout := os.Stdout
	pr, pw, _ := os.Pipe()
	os.Stdout = pw

	err := RunHookCommand(home)
	pw.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("RunHookCommand should not error: %v", err)
	}

	outData := make([]byte, 1024)
	n, _ := pr.Read(outData)
	out := string(outData[:n])
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {}, got %s", out)
	}

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "passthrough") {
		t.Errorf("expected passthrough action in log, got:\n%s", string(data))
	}
}

func TestStatsMissingEvents(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	stats, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.HasEvents {
		t.Error("expected no events")
	}
}

func TestStatsWithEvents(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	os.MkdirAll(filepath.Join(home, "kimi-hooks"), 0755)
	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	lines := []string{
		`{"time":"2026-05-29T10:00:00Z","mode":"observe","action":"observe","original_command":"git status"}`,
		`{"time":"2026-05-29T10:01:00Z","mode":"reroute","action":"reroute","original_command":"go test -v ./..."}`,
		`{"time":"2026-05-29T10:02:00Z","mode":"reroute","action":"reroute","original_command":"go test -v ./..."}`,
		`{"time":"2026-05-29T10:03:00Z","mode":"observe","action":"passthrough","original_command":"git status"}`,
		`{"time":"2026-05-29T10:04:00Z","mode":"observe","action":"error_fail_open","reason":"parse error"}`,
	}
	os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	stats, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.Events != 5 {
		t.Errorf("expected 5 events, got %d", stats.Events)
	}
	if stats.Observed != 1 {
		t.Errorf("expected 1 observed, got %d", stats.Observed)
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
	if stats.TopCommands[0].Command != "go test -v ./..." {
		t.Errorf("expected go test -v ./..., got %s", stats.TopCommands[0].Command)
	}
	if stats.TopCommands[0].Count != 2 {
		t.Errorf("expected count 2, got %d", stats.TopCommands[0].Count)
	}
}

func TestBuildRerouteReasonCompact(t *testing.T) {
	reason := BuildRerouteReason("go test -v ./...", "xit auto go test -v ./...", "compact")
	if !strings.Contains(reason, "XiT:") {
		t.Errorf("expected 'XiT:' in compact reason, got: %s", reason)
	}
	if !strings.Contains(reason, "xit auto go test -v ./...") {
		t.Errorf("expected recommended command in compact reason, got: %s", reason)
	}
	if strings.Contains(reason, "intercepted") {
		t.Error("compact reason should not contain 'intercepted'")
	}
}

func TestBuildRerouteReasonDetailed(t *testing.T) {
	reason := BuildRerouteReason("git diff", "xit auto git diff", "detailed")
	if !strings.Contains(reason, "XiT intercepted") {
		t.Errorf("expected 'XiT intercepted' in detailed reason, got: %s", reason)
	}
	if !strings.Contains(reason, "Recommended rerun") {
		t.Errorf("expected 'Recommended rerun' in detailed reason, got: %s", reason)
	}
	if !strings.Contains(reason, "Why:") {
		t.Errorf("expected 'Why:' in detailed reason, got: %s", reason)
	}
	if !strings.Contains(reason, "Mode:") {
		t.Errorf("expected 'Mode:' in detailed reason, got: %s", reason)
	}
}

func TestSetStatusStyle(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	if err := SetStatusStyle(home, "detailed"); err != nil {
		t.Fatalf("set detailed failed: %v", err)
	}
	cfg, err := ReadHookConfig(home)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if cfg.StatusStyle != "detailed" {
		t.Errorf("expected detailed, got %s", cfg.StatusStyle)
	}
	if err := SetStatusStyle(home, "compact"); err != nil {
		t.Fatalf("set compact failed: %v", err)
	}
	cfg, err = ReadHookConfig(home)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if cfg.StatusStyle != "compact" {
		t.Errorf("expected compact, got %s", cfg.StatusStyle)
	}
}
