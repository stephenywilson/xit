package codexhook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCreatesHooksJSON(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")

	res, err := Install(project, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.HooksPath == "" {
		t.Error("expected hooks path")
	}

	cfg, err := ReadHooksConfig(project)
	if err != nil {
		t.Fatalf("read hooks.json failed: %v", err)
	}
	if !HasXiTHook(cfg) {
		t.Error("expected XiT hook installed")
	}
	if len(cfg.Handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(cfg.Handlers))
	}
	if cfg.Handlers[0].Event != "pre_tool_use" {
		t.Errorf("expected event pre_tool_use, got %s", cfg.Handlers[0].Event)
	}
	if cfg.Handlers[0].Matcher.Tool != "Bash" {
		t.Errorf("expected matcher tool Bash, got %s", cfg.Handlers[0].Matcher.Tool)
	}

	// Script should exist.
	if _, err := os.Stat(res.ScriptPath); err != nil {
		t.Errorf("script not found: %v", err)
	}
}

func TestInstallPreservesExistingHooks(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(project, ".codex"), 0755)
	existing := `{"handlers":[{"event":"pre_tool_use","matcher":{"tool":"Bash"},"command":"/usr/bin/some-hook"}]}` + "\n"
	_ = os.WriteFile(filepath.Join(project, ".codex", "hooks.json"), []byte(existing), 0644)

	_, err := Install(project, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	cfg, err := ReadHooksConfig(project)
	if err != nil {
		t.Fatalf("read hooks.json failed: %v", err)
	}
	if len(cfg.Handlers) != 2 {
		t.Fatalf("expected 2 handlers, got %d", len(cfg.Handlers))
	}
	found := false
	for _, h := range cfg.Handlers {
		if h.Command == "/usr/bin/some-hook" {
			found = true
		}
	}
	if !found {
		t.Error("existing hook was not preserved")
	}
}

func TestUninstallRemovesOnlyXiTHook(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(project, ".codex"), 0755)
	existing := `{"handlers":[{"event":"pre_tool_use","matcher":{"tool":"Bash"},"command":"/usr/bin/some-hook"},{"event":"pre_tool_use","matcher":{"tool":"Bash"},"command":"` + filepath.Join(home, "hooks", "codex-pretooluse-bash.sh") + `"}]}` + "\n"
	_ = os.WriteFile(filepath.Join(project, ".codex", "hooks.json"), []byte(existing), 0644)

	if err := Uninstall(project); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	cfg, err := ReadHooksConfig(project)
	if err != nil {
		t.Fatalf("read hooks.json failed: %v", err)
	}
	if len(cfg.Handlers) != 1 {
		t.Fatalf("expected 1 handler after uninstall, got %d", len(cfg.Handlers))
	}
	if cfg.Handlers[0].Command != "/usr/bin/some-hook" {
		t.Errorf("wrong handler remained: %s", cfg.Handlers[0].Command)
	}
}

func TestRunHookCommandAlreadyWrapped(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	payload := `{"tool_name":"Bash","tool_input":{"command":"xit auto go test -v ./..."},"tool_use_id":"tu-1"}`
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	var out HookOutput
	if err := json.NewDecoder(outR).Decode(&out); err != nil {
		t.Fatalf("invalid hook output: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("expected decision allow, got %s", out.Decision)
	}
	if !strings.Contains(out.StatusMessage, "Codex observe") {
		t.Errorf("expected observe statusMessage, got %s", out.StatusMessage)
	}

	// Check event log.
	data, _ := os.ReadFile(filepath.Join(home, "codex-hooks", "events.jsonl"))
	if !bytes.Contains(data, []byte(`"action":"observe"`)) {
		t.Errorf("expected observe event, got %s", string(data))
	}
	if !bytes.Contains(data, []byte(`"original_command":"xit auto go test -v ./..."`)) {
		t.Errorf("expected original command in event, got %s", string(data))
	}
}

func TestRunHookCommandUnwrappedHighNoise(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	payload := `{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."},"tool_use_id":"tu-1"}`
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	var out HookOutput
	if err := json.NewDecoder(outR).Decode(&out); err != nil {
		t.Fatalf("invalid hook output: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("expected decision allow, got %s", out.Decision)
	}
	if !strings.Contains(out.StatusMessage, "建议使用 xit auto") {
		t.Errorf("expected suggestion statusMessage, got %s", out.StatusMessage)
	}

	data, _ := os.ReadFile(filepath.Join(home, "codex-hooks", "events.jsonl"))
	if !bytes.Contains(data, []byte(`"recommended_command":"xit auto go test -v ./..."`)) {
		t.Errorf("expected recommended command in event, got %s", string(data))
	}
}

func TestRunHookCommandShortCommand(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	payload := `{"tool_name":"Bash","tool_input":{"command":"git status"},"tool_use_id":"tu-1"}`
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	var out HookOutput
	if err := json.NewDecoder(outR).Decode(&out); err != nil {
		t.Fatalf("invalid hook output: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("expected decision allow, got %s", out.Decision)
	}

	data, _ := os.ReadFile(filepath.Join(home, "codex-hooks", "events.jsonl"))
	if !bytes.Contains(data, []byte(`"action":"observe"`)) {
		t.Errorf("expected observe event, got %s", string(data))
	}
}

func TestRunHookCommandFailOpenMalformed(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(`{not json`)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	var out HookOutput
	if err := json.NewDecoder(outR).Decode(&out); err != nil {
		t.Fatalf("invalid hook output: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("expected decision allow for malformed input, got %s", out.Decision)
	}
}

func TestStatsEmpty(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	res, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if res.Events != 0 {
		t.Errorf("expected 0 events, got %d", res.Events)
	}
}

func TestStatsWithEvents(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)
	recs := `{"time":"2026-05-31T12:00:00Z","action":"observe","original_command":"go test"}` + "\n" +
		`{"time":"2026-05-31T12:01:00Z","action":"passthrough","original_command":"git status"}` + "\n" +
		`{"time":"2026-05-31T12:02:00Z","action":"fail_open"}` + "\n"
	_ = os.WriteFile(filepath.Join(home, "codex-hooks", "events.jsonl"), []byte(recs), 0644)

	res, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if res.Events != 3 {
		t.Errorf("expected 3 events, got %d", res.Events)
	}
	if res.Observed != 1 {
		t.Errorf("expected 1 observed, got %d", res.Observed)
	}
	if res.Passthrough != 1 {
		t.Errorf("expected 1 passthrough, got %d", res.Passthrough)
	}
	if res.Errors != 1 {
		t.Errorf("expected 1 error, got %d", res.Errors)
	}
}

func TestStatusInstalled(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")
	_, _ = Install(project, home, false)

	st, err := Status(project, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !st.Installed {
		t.Error("expected installed")
	}
	if st.Mode != "observe" {
		t.Errorf("expected observe mode, got %s", st.Mode)
	}
	if st.Reroute {
		t.Error("expected reroute disabled")
	}
	if !st.FailOpen {
		t.Error("expected fail_open true")
	}
}

func TestStatusNotInstalled(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")

	st, err := Status(project, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if st.Installed {
		t.Error("expected not installed")
	}
}
