package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stephenywilson/xit/internal/shim"
)

func buildXit(t *testing.T) string {
	bin := filepath.Join(t.TempDir(), "xit")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func cleanEnv(cmd *exec.Cmd) {
	cmd.Env = os.Environ()
	cmd.Env = stripEnv(cmd.Env, "XIT_SESSION_ID")
	cmd.Env = stripEnv(cmd.Env, "XIT_SESSION_DIR")
	cmd.Env = append(cmd.Env, "XIT_NONINTERACTIVE=1")
}

func stripEnv(env []string, key string) []string {
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, key+"=") {
			out = append(out, e)
		}
	}
	return out
}

// noXitAdapterEnv returns os.Environ() with XIT_ADAPTER and XIT_OPENCODE_REROUTE_COUNT
// stripped so that tests for the default (non-OpenCode) output path are not affected
// by an outer shell that happens to have XIT_ADAPTER=opencode set.
func noXitAdapterEnv() []string {
	env := stripEnv(os.Environ(), "XIT_ADAPTER")
	env = stripEnv(env, "XIT_OPENCODE_REROUTE_COUNT")
	return env
}

func TestExitCodePreservation(t *testing.T) {
	bin := buildXit(t)

	// Test false returns 1
	cmd := exec.Command(bin, "false")
	cleanEnv(cmd)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("xit false exit code = %d, want 1", exitErr.ExitCode())
		}
	} else {
		t.Errorf("xit false should have failed with exit code 1, got: %v", err)
	}

	// Test true returns 0
	cmd = exec.Command(bin, "true")
	cleanEnv(cmd)
	err = cmd.Run()
	if err != nil {
		t.Errorf("xit true exit code != 0: %v", err)
	}
}

func TestNoNetworkCalls(t *testing.T) {
	// Walk project .go source files (excluding test files) and scan for forbidden network imports / calls
	forbidden := []string{
		"net/http",
		"http.Get",
		"http.Post",
		"http.NewRequest",
		"analytics",
		"upload",
	}
	// Note: "telemetry" is intentionally omitted because the config contains
	// an explicit `telemetry: false` field as a privacy declaration.
	// Actual network telemetry is verified by the absence of net/http above.

	root := "../../"
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		for _, f := range forbidden {
			if strings.Contains(content, f) {
				t.Errorf("forbidden pattern %q found in %s", f, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk error: %v", err)
	}
}

func TestGainJSONWithoutHistory(t *testing.T) {
	bin := buildXit(t)
	cwd := t.TempDir()

	cmd := exec.Command(bin, "gain", "--json")
	cmd.Dir = cwd
	cleanEnv(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit gain --json failed: %v\n%s", err, out)
	}

	var data struct {
		TotalCommandsCondensed int      `json:"total_commands_condensed"`
		RawBytes               int      `json:"raw_bytes"`
		SummaryBytes           int      `json:"summary_bytes"`
		SavedBytes             int      `json:"saved_bytes"`
		EstimatedReduction     float64  `json:"estimated_reduction"`
		SavedTokens            int      `json:"saved_tokens"`
		SavedTokensDisplay     string   `json:"saved_tokens_display"`
		TopCommands            []any    `json:"top_commands"`
		Warnings               []string `json:"warnings"`
		Sources                struct {
			HistoryPath string `json:"history_path"`
			RunsDir     string `json:"runs_dir"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if data.TotalCommandsCondensed != 0 || data.RawBytes != 0 || data.SummaryBytes != 0 || data.SavedBytes != 0 || data.EstimatedReduction != 0 || data.SavedTokens != 0 {
		t.Fatalf("expected zero gain data, got %+v", data)
	}
	if data.SavedTokensDisplay != "0" {
		t.Fatalf("saved_tokens_display = %q, want 0", data.SavedTokensDisplay)
	}
	if data.TopCommands == nil || len(data.TopCommands) != 0 {
		t.Fatalf("top_commands = %#v, want empty array", data.TopCommands)
	}
	if len(data.Warnings) != 1 || data.Warnings[0] != "history not found" {
		t.Fatalf("warnings = %#v, want history not found", data.Warnings)
	}
	if data.Sources.HistoryPath != "" || data.Sources.RunsDir != "" {
		t.Fatalf("sources = %+v, want empty paths", data.Sources)
	}
}

func TestSessionNoArgs(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "session")
	cleanEnv(cmd)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("xit session exit code = %d, want 1", exitErr.ExitCode())
		}
	} else {
		t.Errorf("xit session should fail with exit code 1, got: %v", err)
	}
}

func TestSessionQuietTrue(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "session", "--quiet", "true")
	cleanEnv(cmd)
	err := cmd.Run()
	if err != nil {
		t.Errorf("xit session --quiet true should exit 0: %v", err)
	}
}

func TestSessionEnvVars(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "session", "--quiet", "printenv")
	cleanEnv(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit session --quiet printenv failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XIT_SESSION_ID=") {
		t.Errorf("missing XIT_SESSION_ID in env output:\n%s", out)
	}
	if !strings.Contains(string(out), "XIT_SESSION_DIR=") {
		t.Errorf("missing XIT_SESSION_DIR in env output:\n%s", out)
	}
}

func TestSessionCommandNotFound(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "session", "definitely-not-a-real-command")
	cleanEnv(cmd)
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 127 {
			t.Errorf("xit session not-found exit code = %d, want 127", exitErr.ExitCode())
		}
	} else {
		t.Errorf("xit session not-found should fail with exit code 127, got: %v", err)
	}
	if !strings.Contains(string(out), "command not found") {
		t.Errorf("expected 'command not found' in output, got:\n%s", out)
	}
}

func TestSessionModeAgentGlobal(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "--mode", "agent", "session", "echo", "hello")
	cleanEnv(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit --mode agent session echo hello failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "[xit:session start") {
		t.Errorf("expected agent start banner, got:\n%s", out)
	}
	if !strings.Contains(string(out), "[xit:session end") {
		t.Errorf("expected agent end report, got:\n%s", out)
	}
}

func TestSessionModeAgentLocal(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "session", "--mode", "agent", "echo", "hello")
	cleanEnv(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit session --mode agent echo hello failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "[xit:session start") {
		t.Errorf("expected agent start banner, got:\n%s", out)
	}
	if !strings.Contains(string(out), "[xit:session end") {
		t.Errorf("expected agent end report, got:\n%s", out)
	}
}

func TestSessionModeJSONLocal(t *testing.T) {
	bin := buildXit(t)

	cmd := exec.Command(bin, "session", "--mode", "json", "echo", "hello")
	cleanEnv(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit session --mode json echo hello failed: %v\n%s", err, out)
	}
	// The output will contain the child "hello" line plus JSON events.
	// We parse each line to find the events, since map key order is not guaranteed.
	lines := strings.Split(string(out), "\n")
	var hasStart, hasEnd bool
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "hello" {
			continue
		}
		var event struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Event == "session_start" {
			hasStart = true
		}
		if event.Event == "session_end" {
			hasEnd = true
		}
	}
	if !hasStart {
		t.Errorf("missing json start event in output:\n%s", out)
	}
	if !hasEnd {
		t.Errorf("missing json end event in output:\n%s", out)
	}
}

func TestVersionOutput(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit --version failed: %v", err)
	}
	if !strings.Contains(string(out), "0.2.45") {
		t.Errorf("expected version 0.2.45, got: %s", out)
	}
}

func TestDoctorDoesNotWriteFiles(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "doctor")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit doctor failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Doctor") {
		t.Errorf("expected doctor header, got:\n%s", out)
	}
	// Verify no config file was created
	if _, err := os.Stat(filepath.Join(tmpHome, ".xit", "config.json")); err == nil {
		t.Error("doctor should not create config.json")
	}
}

func TestInitCreatesConfig(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit init failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT initialized") {
		t.Errorf("expected init message, got:\n%s", out)
	}

	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.json not created: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid config JSON: %v", err)
	}
	if cfg["telemetry"] != false {
		t.Errorf("telemetry should be false, got %v", cfg["telemetry"])
	}
}

func TestInitDoesNotOverwriteWithoutForce(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// First init
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Second init without --force should fail
	cmd = exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("second init should fail without --force")
	}
	if !strings.Contains(string(out), "already exists") {
		t.Errorf("expected 'already exists' error, got:\n%s", out)
	}
}

func TestInitForceOverwrites(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// First init
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Modify config to mark a difference
	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	os.WriteFile(configPath, []byte(`{"version":"old"}`), 0644)

	// Force init should overwrite
	cmd = exec.Command(bin, "init", "--force")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init --force failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT initialized") {
		t.Errorf("expected init message, got:\n%s", out)
	}
}

func TestInitKimiNotFound(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	// Create a minimal PATH without kimi
	tmpPath := t.TempDir()
	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("init kimi should fail when kimi not found")
	}
	if !strings.Contains(string(out), "not found") {
		t.Errorf("expected 'not found' error, got:\n%s", out)
	}
}

func TestInitKimiWithSimulatedPath(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpPath := t.TempDir()
	kimiPath := filepath.Join(tmpPath, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho hello"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init kimi failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT initialized for Kimi") {
		t.Errorf("expected init kimi message, got:\n%s", out)
	}

	// Verify config was updated
	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	data, _ := os.ReadFile(configPath)
	var cfg map[string]interface{}
	json.Unmarshal(data, &cfg)
	targets := cfg["targets"].(map[string]interface{})
	kimi := targets["kimi"].(map[string]interface{})
	if kimi["enabled"] != true {
		t.Errorf("kimi should be enabled in config")
	}
}

func TestConfigMissing(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "config")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("config should fail when not initialized")
	}
	if !strings.Contains(string(out), "xit init") {
		t.Errorf("expected 'xit init' hint, got:\n%s", out)
	}
}

func TestConfigShowsSummary(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// Init first
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "config")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("config failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Config") {
		t.Errorf("expected config header, got:\n%s", out)
	}
}

func TestWrapperNotInitialized(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("kimi wrapper should fail when not initialized")
	}
	if !strings.Contains(string(out), "xit init kimi") {
		t.Errorf("expected 'xit init kimi' hint, got:\n%s", out)
	}
}

func TestWrapperTargetPathMissing(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// Init with a fake target path that doesn't exist
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	os.WriteFile(configPath, []byte(`{"version":"0.2.8","default_mode":"agent","token_estimator":"bytes/4","telemetry":false,"targets":{"claude":{"enabled":true,"path":"/nonexistent/claude","integration":"wrapper","wrapper":true}}}`), 0644)

	cmd = exec.Command(bin, "claude")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("claude wrapper should fail when path missing")
	}
	if !strings.Contains(string(out), "not found") {
		t.Errorf("expected 'not found' error, got:\n%s", out)
	}
}

func TestShimStatusNoConfig(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "shim", "status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("shim status should fail without config")
	}
	if !strings.Contains(string(out), "xit init") {
		t.Errorf("expected init hint, got:\n%s", out)
	}
}

func TestShimStatusReadOnly(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Shim Status") {
		t.Errorf("expected shim status header, got:\n%s", out)
	}
	if !strings.Contains(string(out), "not configured") {
		t.Errorf("expected not configured, got:\n%s", out)
	}
}

func TestShimInstallRequiresYes(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "install", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("shim install without --yes should fail")
	}
	if !strings.Contains(string(out), "--yes") {
		t.Errorf("expected --yes error, got:\n%s", out)
	}
}

func TestShimInstallCreatesShim(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpPath := t.TempDir()
	kimiPath := filepath.Join(tmpPath, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho hello"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim install failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT shim installed") {
		t.Errorf("expected install message, got:\n%s", out)
	}

	shimPath := filepath.Join(tmpHome, ".local", "bin", "kimi")
	if _, err := os.Stat(shimPath); err != nil {
		t.Fatalf("shim not created at %s", shimPath)
	}

	data, _ := os.ReadFile(shimPath)
	if !strings.Contains(string(data), "# XiT shim managed file") {
		t.Error("shim missing XiT marker")
	}

	// Verify config
	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	cfgData, _ := os.ReadFile(configPath)
	var cfg map[string]interface{}
	json.Unmarshal(cfgData, &cfg)
	targets := cfg["targets"].(map[string]interface{})
	kimi := targets["kimi"].(map[string]interface{})
	if kimi["shim_enabled"] != true {
		t.Errorf("shim_enabled should be true")
	}
	if kimi["original_path"] != kimiPath {
		t.Errorf("original_path mismatch: got %v", kimi["original_path"])
	}
}

func TestShimInstallDoesNotOverwriteNonShim(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpPath := t.TempDir()
	kimiPath := filepath.Join(tmpPath, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho hello"), 0755)

	// Pre-create a non-shim file
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho not xit"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("shim install should fail when non-shim exists")
	}
	if !strings.Contains(string(out), "not a XiT shim") {
		t.Errorf("expected not-a-shim error, got:\n%s", out)
	}
}

func TestShimRemoveDeletesShim(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpPath := t.TempDir()
	kimiPath := filepath.Join(tmpPath, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho hello"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "remove", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpPath, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim remove failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT shim removed") {
		t.Errorf("expected remove message, got:\n%s", out)
	}

	shimPath := filepath.Join(tmpHome, ".local", "bin", "kimi")
	if _, err := os.Stat(shimPath); err == nil {
		t.Error("shim should be removed")
	}
}

func TestShimRemoveDoesNotDeleteNonShim(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte("#!/bin/sh\necho not xit"), 0755)

	// Set up config pointing to this non-shim file
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	os.WriteFile(configPath, []byte(`{"version":"0.2.2","default_mode":"agent","token_estimator":"bytes/4","telemetry":false,"targets":{"kimi":{"enabled":true,"path":"/usr/bin/kimi","shim_enabled":true,"shim_path":"`+shimPath+`","integration":"shim"}}}`), 0644)

	cmd = exec.Command(bin, "shim", "remove", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("shim remove should fail for non-managed file")
	}
	if !strings.Contains(string(out), "not a XiT managed shim") {
		t.Errorf("expected managed-shim error, got:\n%s", out)
	}
}

func TestShimInstallNeedsInit(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "shim", "install", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("shim install should fail without init")
	}
	if !strings.Contains(string(out), "xit init") {
		t.Errorf("expected init hint, got:\n%s", out)
	}
}

func TestWrapperUsesOriginalPath(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpPath := t.TempDir()

	// Create a fake original claude
	originalPath := filepath.Join(tmpPath, "original-claude")
	os.WriteFile(originalPath, []byte("#!/bin/sh\necho 'from original'"), 0755)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Manually set config with original_path pointing to our fake binary
	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	os.WriteFile(configPath, []byte(`{"version":"0.2.8","default_mode":"agent","token_estimator":"bytes/4","telemetry":false,"targets":{"claude":{"enabled":true,"path":"/nonexistent/claude","original_path":"`+originalPath+`","integration":"wrapper","wrapper":true}}}`), 0644)

	// Wrapper should use original_path and succeed
	cmd = exec.Command(bin, "claude")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// wrapper starts a session which may exit non-zero if the fake binary does
		// but we should at least see the wrapper banner with the original path
	}
	if !strings.Contains(string(out), "original:") {
		t.Errorf("expected original path in wrapper output, got:\n%s", out)
	}
	if strings.Contains(string(out), "/nonexistent/claude") {
		t.Error("wrapper should not use path when original_path exists")
	}
}

func TestAutoCommand(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\nfor i in $(seq 1 200); do echo \"+ line $i changed in src/example.go\"; done"), 0755)

	cmd := exec.Command(bin, "auto", "git", "diff")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_ORIGINAL_GIT="+gitPath, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// auto may return non-zero if git returns non-zero, but here git succeeds
		t.Fatalf("auto git diff failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T完成") {
		t.Errorf("expected 吸T完成 header, got:\n%s", out)
	}
}

func TestAutoPassthroughSmallOutput(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\necho 'small output'"), 0755)

	cmd := exec.Command(bin, "auto", "git", "status")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_ORIGINAL_GIT="+gitPath, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto git status failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "吸T完成") {
		t.Error("small output should passthrough without auto summary")
	}
	if !strings.Contains(string(out), "small output") {
		t.Errorf("expected passthrough output, got:\n%s", out)
	}
}

func TestAutoWritesRuntimeState(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\nfor i in $(seq 1 200); do echo \"+ line $i changed in src/example.go\"; done"), 0755)

	cmd := exec.Command(bin, "auto", "git", "diff")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_ORIGINAL_GIT="+gitPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto git diff failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T完成") {
		t.Errorf("expected 吸T完成 header, got:\n%s", out)
	}

	statePath := filepath.Join(tmpHome, "state", "current-run.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("expected state file at %s: %v", statePath, err)
	}
	if !strings.Contains(string(data), `"status":"completed"`) {
		t.Errorf("expected completed status in state file, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"saved_bytes"`) {
		t.Errorf("expected saved_bytes in state file, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"raw_log"`) {
		t.Errorf("expected raw_log in state file, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"heartbeat_at"`) {
		t.Errorf("expected heartbeat_at in state file, got: %s", string(data))
	}
}

func TestAutoStateFailOpen(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	// Make state directory read-only to force write failure
	stateDir := filepath.Join(tmpHome, "state")
	_ = os.MkdirAll(stateDir, 0755)
	_ = os.Chmod(stateDir, 0555)
	defer os.Chmod(stateDir, 0755)

	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\necho 'small output'"), 0755)

	cmd := exec.Command(bin, "auto", "git", "status")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_ORIGINAL_GIT="+gitPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto git status should succeed even when state write fails: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "small output") {
		t.Errorf("expected passthrough output, got:\n%s", out)
	}
}

func TestSessionCreatesShims(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	// Create a fake command that prints env
	fakePath := filepath.Join(tmpHome, "fakecmd")
	os.WriteFile(fakePath, []byte("#!/bin/sh\nprintenv"), 0755)
	os.Chmod(fakePath, 0755)

	cmd := exec.Command(bin, "session", "--quiet", fakePath)
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// fakecmd exits 0, should be fine
	}

	// The session should have created shims and injected PATH.
	// Check that PATH contains a shim directory.
	if !strings.Contains(string(out), "/shims") {
		t.Errorf("expected shim dir in PATH env, got:\n%s", out)
	}
}

func TestSessionNoAutoShims(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	fakePath := filepath.Join(tmpHome, "fakecmd")
	os.WriteFile(fakePath, []byte("#!/bin/sh\nprintenv"), 0755)
	os.Chmod(fakePath, 0755)

	cmd := exec.Command(bin, "session", "--quiet", "--no-auto-shims", fakePath)
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// fakecmd exits 0
	}

	// With --no-auto-shims, PATH should NOT contain a shim directory.
	if strings.Contains(string(out), "/shims") {
		t.Errorf("expected no shim dir in PATH with --no-auto-shims, got:\n%s", out)
	}
}

func TestShimInstallTakeover(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	// Put original kimi directly at ~/.local/bin/kimi
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	kimiPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho original kimi"), 0755)

	// Init kimi (path will be the same as shim target)
	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Without --takeover should fail
	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("shim install without takeover should fail when original==shim")
	}
	if !strings.Contains(string(out), "--takeover") {
		t.Errorf("expected --takeover hint, got:\n%s", out)
	}

	// With --takeover --force-unsafe-tui should succeed
	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes", "--takeover", "--force-unsafe-tui")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim install takeover failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Takeover mode") {
		t.Errorf("expected takeover message, got:\n%s", out)
	}

	// Backup should exist
	backupPath := kimiPath + ".xit-original"
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup not found at %s", backupPath)
	}

	// Shim should be XiT managed
	if !shim.IsManagedShim(kimiPath) {
		t.Error("kimi should be a XiT shim after takeover")
	}

	// Config should show takeover
	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), `"takeover": true`) {
		t.Errorf("config should show takeover=true, got:\n%s", data)
	}

	// Remove should restore original
	cmd = exec.Command(bin, "shim", "remove", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim remove failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "restored") {
		t.Errorf("expected restored message, got:\n%s", out)
	}

	// Original should be back
	if shim.IsManagedShim(kimiPath) {
		t.Error("kimi should be restored original, not a XiT shim")
	}
	data, _ = os.ReadFile(kimiPath)
	if !strings.Contains(string(data), "original kimi") {
		t.Errorf("restored file wrong content: %s", data)
	}
}

func TestClaudeHookDryRun(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "init", "claude", "--method", "official_hook", "--dry-run")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Claude Code official hook") {
		t.Errorf("expected official hook plan, got:\n%s", out)
	}
	_, err = os.Stat(filepath.Join(tmpHome, ".xit", "config.json"))
	if err == nil {
		t.Error("dry-run should not create config")
	}
}

func TestClaudeHookInstall(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	cmd := exec.Command(bin, "init", "claude", "--method", "official_hook", "--scope", "project", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "official_hook") {
		t.Errorf("expected official_hook integration, got:\n%s", out)
	}

	settingsPath := filepath.Join(tmpCwd, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}
	if !strings.Contains(string(data), "PreToolUse") {
		t.Error("expected PreToolUse in settings")
	}
	if !strings.Contains(string(data), "claude-pretooluse-bash.sh") {
		t.Error("expected hook script in settings")
	}

	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	cfgData, _ := os.ReadFile(configPath)
	if !strings.Contains(string(cfgData), "official_hook") {
		t.Errorf("expected official_hook in config, got:\n%s", cfgData)
	}
}

func TestClaudeHookStatus(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()

	// Install first
	cmd := exec.Command(bin, "init", "claude", "--method", "official_hook", "--scope", "project", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "status", "claude")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installed:  yes") {
		t.Errorf("expected installed yes, got:\n%s", out)
	}
	if !strings.Contains(string(out), "mode:       observe") {
		t.Errorf("expected observe mode, got:\n%s", out)
	}
}

func TestClaudeHookUninstall(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()

	// Install first
	cmd := exec.Command(bin, "init", "claude", "--method", "official_hook", "--scope", "project", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "uninstall", "claude", "--method", "official_hook", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "uninstalled") {
		t.Errorf("expected uninstalled message, got:\n%s", out)
	}

	settingsPath := filepath.Join(tmpCwd, ".claude", "settings.json")
	data, _ := os.ReadFile(settingsPath)
	if strings.Contains(string(data), "claude-pretooluse-bash.sh") {
		t.Error("expected XiT hook removed from settings")
	}
}

func TestClaudeHookEvent(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "claude-hook", "pretooluse-bash")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude-hook failed: %v\n%s", err, out)
	}
	if string(out) != "{}\n" {
		t.Errorf("expected empty JSON, got: %s", out)
	}

	eventsPath := filepath.Join(tmpHome, ".xit", "claude-hooks", "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("events.jsonl not created: %v", err)
	}
	if !strings.Contains(string(data), "go test") {
		t.Errorf("expected event logged, got:\n%s", data)
	}
}

func TestHookEnableRerouteRequiresYes(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "enable-reroute", "claude")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if !strings.Contains(string(out), "--yes") {
		t.Errorf("expected --yes requirement, got:\n%s", out)
	}
}

func TestHookEnableDisableReroute(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "hook", "enable-reroute", "claude", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("enable-reroute failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "enabled") {
		t.Errorf("expected enabled message, got:\n%s", out)
	}

	configPath := filepath.Join(tmpHome, ".xit", "claude-hooks", "config.json")
	data, _ := os.ReadFile(configPath)
	if !strings.Contains(string(data), "reroute") {
		t.Errorf("expected reroute in config, got:\n%s", string(data))
	}

	cmd = exec.Command(bin, "hook", "disable-reroute", "claude", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("disable-reroute failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "disabled") {
		t.Errorf("expected disabled message, got:\n%s", out)
	}

	data, _ = os.ReadFile(configPath)
	if !strings.Contains(string(data), "observe") {
		t.Errorf("expected observe in config, got:\n%s", string(data))
	}
}

func TestHookRerouteReturnsDeny(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// Enable reroute first
	cmd := exec.Command(bin, "hook", "enable-reroute", "claude", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Test go test -v reroute
	cmd = exec.Command(bin, "claude-hook", "pretooluse-bash")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude-hook failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "deny") {
		t.Errorf("expected deny in output, got: %s", out)
	}
	if !strings.Contains(string(out), "xit auto go test -v ./...") {
		t.Errorf("expected recommended command in output, got: %s", out)
	}
}

func TestHookReroutePassthroughGitStatus(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "hook", "enable-reroute", "claude", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "claude-hook", "pretooluse-bash")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude-hook failed: %v\n%s", err, out)
	}
	if string(out) != "{}\n" {
		t.Errorf("expected empty JSON for git status passthrough, got: %s", out)
	}
}

func TestHookStats(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// Enable reroute and generate events
	cmd := exec.Command(bin, "hook", "enable-reroute", "claude", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "claude-hook", "pretooluse-bash")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`)
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "claude-hook", "pretooluse-bash")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"git status"}}`)
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "stats", "claude")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook stats failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "events:") {
		t.Errorf("expected events header, got:\n%s", out)
	}
}

func TestHookStatsMissing(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "stats", "claude")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook stats failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No hook events recorded yet") {
		t.Errorf("expected no-events message, got:\n%s", out)
	}
}

func TestKimiTakeoverRefusedByDefault(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	kimiPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho original kimi"), 0755)

	// Init kimi
	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Takeover without --force-unsafe-tui should fail
	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes", "--takeover")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("kimi takeover should be refused by default")
	}
	if !strings.Contains(string(out), "disabled by default") {
		t.Errorf("expected disabled by default message, got:\n%s", out)
	}
	if !strings.Contains(string(out), "force-unsafe-tui") {
		t.Errorf("expected force-unsafe-tui hint, got:\n%s", out)
	}
}

func TestKimiTakeoverForceUnsafeWorks(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	kimiPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho original kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes", "--takeover", "--force-unsafe-tui")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi takeover with force-unsafe-tui failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Takeover mode") {
		t.Errorf("expected takeover message, got:\n%s", out)
	}

	// Remove should restore
	cmd = exec.Command(bin, "shim", "remove", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shim remove failed: %v\n%s", err, out)
	}
	if !shim.IsManagedShim(kimiPath) {
		// restored
	} else {
		t.Error("kimi should be restored after remove")
	}
}

func TestKimiWrapperShowsWarning(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	// Init with fake kimi path
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	os.WriteFile(configPath, []byte(`{"version":"0.2.8","default_mode":"agent","token_estimator":"bytes/4","telemetry":false,"targets":{"kimi":{"enabled":true,"path":"/bin/echo","original_path":"/bin/echo","integration":"wrapper","wrapper":true}}}`), 0644)

	// xit kimi without --unsafe-pty should warn and exit
	cmd = exec.Command(bin, "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("xit kimi should fail without --unsafe-pty")
	}
	if !strings.Contains(string(out), "compatibility warning") {
		t.Errorf("expected compatibility warning, got:\n%s", out)
	}
	if !strings.Contains(string(out), "--unsafe-pty") {
		t.Errorf("expected --unsafe-pty hint, got:\n%s", out)
	}
}

func TestDoctorKimiCompatibility(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "doctor", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor kimi failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Kimi Compatibility") {
		t.Errorf("expected Kimi Compatibility header, got:\n%s", out)
	}
	if !strings.Contains(string(out), "takeover") {
		t.Errorf("expected takeover info, got:\n%s", out)
	}
	if !strings.Contains(string(out), "manual") {
		t.Errorf("expected manual recommendation, got:\n%s", out)
	}
}

func TestNonKimiTakeoverStillWorks(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	// Use codex as non-Kimi target
	codexPath := filepath.Join(shimDir, "codex")
	os.WriteFile(codexPath, []byte("#!/bin/sh\necho original codex"), 0755)

	cmd := exec.Command(bin, "init", "codex", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Codex takeover should still work without --force-unsafe-tui
	cmd = exec.Command(bin, "shim", "install", "codex", "--yes", "--takeover")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex takeover should work: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Takeover mode") {
		t.Errorf("expected takeover message, got:\n%s", out)
	}
}

func TestKimiHookStatusNotInstalled(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	cmd := exec.Command(bin, "hook", "status", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installed:  no") {
		t.Errorf("expected not installed, got:\n%s", out)
	}
	if !strings.Contains(string(out), "mode:       observe") {
		t.Errorf("expected observe mode, got:\n%s", out)
	}
}

func TestKimiHookInstallDryRun(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--dry-run")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+tmpHome, "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "official_hook") {
		t.Errorf("expected official hook plan, got:\n%s", out)
	}
	_, err = os.Stat(filepath.Join(tmpCwd, ".kimi", "config.toml"))
	if err == nil {
		t.Error("dry-run should not create config")
	}
}

func TestKimiHookInstallProjectScope(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)
	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "project", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "official_hook") {
		t.Errorf("expected official_hook integration, got:\n%s", out)
	}

	configPath := filepath.Join(tmpCwd, ".kimi", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.toml not created: %v", err)
	}
	if !strings.Contains(string(data), "[[hooks]]") {
		t.Error("expected [[hooks]] in TOML config")
	}
	if !strings.Contains(string(data), `matcher = "Shell"`) {
		t.Error("expected Shell matcher in TOML config")
	}
	if !strings.Contains(string(data), `matcher = "Bash"`) {
		t.Error("expected Bash matcher in TOML config")
	}
	if !strings.Contains(string(data), "kimi-pretooluse-shell.sh") {
		t.Error("expected hook script in config")
	}

	xitConfigPath := filepath.Join(tmpHome, ".xit", "config.json")
	cfgData, _ := os.ReadFile(xitConfigPath)
	if !strings.Contains(string(cfgData), "official_hook") {
		t.Errorf("expected official_hook in xit config, got:\n%s", cfgData)
	}
}

func TestKimiHookObserveFailOpen(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi-hook", "observe")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader("not json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-hook failed: %v\n%s", err, out)
	}
	if string(out) != "{}\n" {
		t.Errorf("expected empty JSON, got: %s", out)
	}

	eventsPath := filepath.Join(tmpHome, ".xit", "kimi-hooks", "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("events.jsonl not created: %v", err)
	}
	if !strings.Contains(string(data), "error_fail_open") {
		t.Errorf("expected error_fail_open event, got:\n%s", data)
	}
}

func TestKimiHookObserveLogsBash(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi-hook", "observe")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-hook failed: %v\n%s", err, out)
	}
	if string(out) != "{}\n" {
		t.Errorf("expected empty JSON, got: %s", out)
	}

	eventsPath := filepath.Join(tmpHome, ".xit", "kimi-hooks", "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("events.jsonl not created: %v", err)
	}
	if !strings.Contains(string(data), "go test -v ./...") {
		t.Errorf("expected event logged, got:\n%s", data)
	}
	if !strings.Contains(string(data), "observe") {
		t.Errorf("expected observe action, got:\n%s", data)
	}
	if strings.Contains(string(data), "deny") {
		t.Errorf("observe mode must not deny, got:\n%s", data)
	}
}

func TestKimiWrapperStillBlocked(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	configPath := filepath.Join(tmpHome, ".xit", "config.json")
	os.MkdirAll(filepath.Dir(configPath), 0755)
	os.WriteFile(configPath, []byte(`{"version":"0.2.12","default_mode":"agent","token_estimator":"bytes/4","telemetry":false,"targets":{"kimi":{"enabled":true,"path":"/bin/echo","original_path":"/bin/echo","integration":"wrapper","wrapper":true}}}`), 0644)

	cmd := exec.Command(bin, "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("xit kimi should fail without --unsafe-pty")
	}
	if !strings.Contains(string(out), "compatibility warning") {
		t.Errorf("expected compatibility warning, got:\n%s", out)
	}
}

func TestKimiTakeoverStillRefused(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	kimiPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho original kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes", "--takeover")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("kimi takeover should be refused by default")
	}
	if !strings.Contains(string(out), "disabled by default") {
		t.Errorf("expected disabled by default message, got:\n%s", out)
	}
}

func TestKimiHookUninstall(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "project", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "uninstall", "kimi", "--method", "official_hook", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "uninstalled") {
		t.Errorf("expected uninstalled message, got:\n%s", out)
	}

	configPath := filepath.Join(tmpCwd, ".kimi", "config.toml")
	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), "kimi-pretooluse-shell.sh") {
		t.Error("expected XiT hook removed from config")
	}
}

func TestDoctorKimiDeepDoesNotWriteFiles(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	cmd := exec.Command(bin, "doctor", "kimi", "--deep")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor kimi --deep failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Kimi Health Check") {
		t.Errorf("expected health check header, got:\n%s", out)
	}
	// Ensure no files were written.
	if _, err := os.Stat(filepath.Join(tmpCwd, ".kimi", "config.toml")); err == nil {
		t.Error("deep doctor should not create config")
	}
}

func TestDoctorKimiDeepReportsConfigs(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	// Create a project TOML config with XiT hook.
	os.MkdirAll(filepath.Join(tmpCwd, ".kimi"), 0755)
	os.WriteFile(filepath.Join(tmpCwd, ".kimi", "config.toml"), []byte("[[hooks]]\ncommand = \"/home/user/.xit/hooks/kimi-pretooluse-shell.sh\"\n"), 0644)

	cmd := exec.Command(bin, "doctor", "kimi", "--deep")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor kimi --deep failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), ".kimi/config.toml") {
		t.Errorf("expected project config path, got:\n%s", out)
	}
	if !strings.Contains(string(out), "Kimi:") {
		t.Errorf("expected Kimi section, got:\n%s", out)
	}
	if !strings.Contains(string(out), "Hook observe:") {
		t.Errorf("expected Hook observe section, got:\n%s", out)
	}
}

func TestHookTestKimiWritesEvent(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "test", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook test kimi failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Kimi Hook Self-Test") {
		t.Errorf("expected self-test header, got:\n%s", out)
	}
	// Script does not exist yet, so result should mention not found.
	if !strings.Contains(string(out), "not found") && !strings.Contains(string(out), "hook script not found") {
		t.Errorf("expected not-found message when hook not installed, got:\n%s", out)
	}
}

func TestHookTestKimiAfterInstall(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	// Install hook first.
	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "project", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	cmd.CombinedOutput()

	// Run self-test.
	cmd = exec.Command(bin, "hook", "test", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook test kimi failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "result: XiT hook command works locally") {
		t.Errorf("expected local success message, got:\n%s", out)
	}
}

func TestKimiInstructionsIncludesHooks(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "kimi-instructions")
	cmd.Env = append(os.Environ(), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-instructions failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "/hooks") {
		t.Errorf("expected /hooks mention, got:\n%s", out)
	}
	if !strings.Contains(string(out), "events.jsonl") {
		t.Errorf("expected events.jsonl mention, got:\n%s", out)
	}
	if !strings.Contains(string(out), "--scope user") {
		t.Errorf("expected user-scope fallback command, got:\n%s", out)
	}
	if strings.Contains(string(out), "takeover") {
		t.Error("instructions should not mention takeover")
	}
}

func TestKimiHookStatusUserScope(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	// Create user-scope config.
	os.MkdirAll(filepath.Join(tmpHome, ".kimi"), 0755)
	os.WriteFile(filepath.Join(tmpHome, ".kimi", "config.toml"), []byte("[[hooks]]\nevent = \"PreToolUse\"\nmatcher = \"Shell\"\ncommand = \"/home/user/.xit/hooks/kimi-pretooluse-shell.sh\"\n"), 0644)

	cmd := exec.Command(bin, "hook", "status", "kimi", "--scope", "user")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook status user scope failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "scope:      user") {
		t.Errorf("expected user scope, got:\n%s", out)
	}
	if !strings.Contains(string(out), "installed:  yes") {
		t.Errorf("expected installed yes, got:\n%s", out)
	}
}

func TestKimiUninstallUserScope(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	// Create user-scope config.
	os.MkdirAll(filepath.Join(tmpHome, ".kimi"), 0755)
	os.WriteFile(filepath.Join(tmpHome, ".kimi", "config.toml"), []byte("[[hooks]]\nevent = \"PreToolUse\"\nmatcher = \"Shell\"\ncommand = \"/home/user/.xit/hooks/kimi-pretooluse-shell.sh\"\n"), 0644)
	// Create XiT config with kimi enabled official_hook.
	os.MkdirAll(filepath.Join(tmpHome, ".xit"), 0755)
	os.WriteFile(filepath.Join(tmpHome, ".xit", "config.json"), []byte(`{"version":"0.2.12","default_mode":"agent","token_estimator":"bytes/4","telemetry":false,"targets":{"kimi":{"enabled":true,"path":"/bin/echo","integration":"official_hook"}}}`), 0644)

	cmd := exec.Command(bin, "uninstall", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall user scope failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "uninstalled") {
		t.Errorf("expected uninstalled message, got:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(tmpHome, ".kimi", "config.toml"))
	if strings.Contains(string(data), "kimi-pretooluse-shell.sh") {
		t.Error("expected XiT hook removed from user-scope config")
	}
}

func TestKimiHookInstallUserScopeWithEmptyHooksArray(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	// Create user config with empty hooks array.
	os.MkdirAll(filepath.Join(tmpHome, ".kimi"), 0755)
	os.WriteFile(filepath.Join(tmpHome, ".kimi", "config.toml"), []byte("default_model = \"kimi\"\nhooks = []\n\n[provider]\nmodel = \"kimi\"\n"), 0644)

	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}

	data, _ := os.ReadFile(filepath.Join(tmpHome, ".kimi", "config.toml"))
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

	// Status should show installed and no conflict warning.
	cmd = exec.Command(bin, "hook", "status", "kimi", "--scope", "user")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installed:  yes") {
		t.Errorf("expected installed yes, got:\n%s", out)
	}
	if strings.Contains(string(out), "warning: config contains both hooks = []") {
		t.Errorf("expected no conflict warning after fix, got:\n%s", out)
	}

	// Uninstall should work cleanly.
	cmd = exec.Command(bin, "uninstall", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "uninstalled") {
		t.Errorf("expected uninstalled message, got:\n%s", out)
	}
}

func TestKimiResponseSchema(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "kimi", "response-schema")
	cmd.Env = append(os.Environ(), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi response-schema failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Response Schema Discovery") {
		t.Errorf("expected discovery header, got:\n%s", out)
	}
	if !strings.Contains(string(out), "observe hook:           verified") {
		t.Errorf("expected observe verified, got:\n%s", out)
	}
	if !strings.Contains(string(out), "block/deny:             supported") {
		t.Errorf("expected block supported, got:\n%s", out)
	}
	if !strings.Contains(string(out), "command rewrite:        unsupported") {
		t.Errorf("expected rewrite unsupported, got:\n%s", out)
	}
	if !strings.Contains(string(out), "NOT YET IMPLEMENTED in XiT") {
		t.Errorf("expected reroute not implemented, got:\n%s", out)
	}
	if strings.Contains(string(out), "takeover") {
		t.Error("response-schema should not mention takeover")
	}
}

func TestDoctorKimiDeepIncludesSchema(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpCwd := t.TempDir()
	cmd := exec.Command(bin, "doctor", "kimi", "--deep")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Dir = tmpCwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor kimi --deep failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Hook observe:") {
		t.Errorf("expected hook observe section, got:\n%s", out)
	}
	if !strings.Contains(string(out), "mode:") {
		t.Errorf("expected mode field in doctor, got:\n%s", out)
	}
	if !strings.Contains(string(out), "reroute:") {
		t.Errorf("expected reroute field in doctor, got:\n%s", out)
	}
	if !strings.Contains(string(out), "Verdict:") {
		t.Errorf("expected verdict section, got:\n%s", out)
	}
}

func TestKimiHookEnableRerouteWithoutYes(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "hook", "enable-reroute", "kimi")
	cmd.Env = append(os.Environ(), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if !strings.Contains(string(out), "--yes") {
		t.Errorf("expected --yes requirement message, got:\n%s", out)
	}
}

func TestKimiHookEnableRerouteWithYes(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "enable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("enable-reroute failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "enabled") {
		t.Errorf("expected enabled message, got:\n%s", out)
	}

	// Verify config written.
	data, _ := os.ReadFile(filepath.Join(tmpHome, ".xit", "kimi-hooks", "config.json"))
	if !strings.Contains(string(data), "reroute") {
		t.Errorf("expected reroute in config, got:\n%s", string(data))
	}
}

func TestKimiHookDisableRerouteWithYes(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	// Enable first.
	cmd := exec.Command(bin, "hook", "enable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Then disable.
	cmd = exec.Command(bin, "hook", "disable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("disable-reroute failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "disabled") {
		t.Errorf("expected disabled message, got:\n%s", out)
	}
}

func TestKimiHookStats(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "stats", "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stats failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Kimi Hook Stats") {
		t.Errorf("expected stats header, got:\n%s", out)
	}
	if !strings.Contains(string(out), "No hook events recorded yet") {
		t.Errorf("expected no-events message, got:\n%s", out)
	}
}

func TestKimiHookStatusShowsMode(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	// Install hook.
	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Enable reroute.
	cmd = exec.Command(bin, "hook", "enable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Check status shows reroute enabled.
	cmd = exec.Command(bin, "hook", "status", "kimi", "--scope", "user")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "mode:       reroute") {
		t.Errorf("expected reroute mode, got:\n%s", out)
	}
	if !strings.Contains(string(out), "reroute:    enabled") {
		t.Errorf("expected reroute enabled, got:\n%s", out)
	}

	// Disable and check observe.
	cmd = exec.Command(bin, "hook", "disable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "status", "kimi", "--scope", "user")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status after disable failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "mode:       observe") {
		t.Errorf("expected observe mode, got:\n%s", out)
	}
	if !strings.Contains(string(out), "reroute:    disabled") {
		t.Errorf("expected reroute disabled, got:\n%s", out)
	}
}

func TestKimiHookRerouteViaObserveCommand(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	// Install hook.
	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Enable reroute.
	cmd = exec.Command(bin, "hook", "enable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Run hook observe with go test payload.
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	cmd = exec.Command(bin, "kimi-hook", "observe")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-hook observe failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "permissionDecision") {
		t.Errorf("expected deny response with permissionDecision, got:\n%s", out)
	}
	if !strings.Contains(string(out), "xit auto go test -v ./...") {
		t.Errorf("expected recommended command, got:\n%s", out)
	}

	// Check event log.
	logPath := filepath.Join(tmpHome, ".xit", "kimi-hooks", "events.jsonl")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), `"action":"reroute"`) {
		t.Errorf("expected reroute action in event log, got:\n%s", string(data))
	}
}

func TestKimiHookStatusStyleWithoutYes(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "hook", "status-style", "kimi", "compact")
	cmd.Env = append(os.Environ(), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if !strings.Contains(string(out), "--yes") {
		t.Errorf("expected --yes requirement, got:\n%s", out)
	}
}

func TestKimiHookStatusStyleCompact(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "status-style", "kimi", "compact", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status-style failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "compact") {
		t.Errorf("expected compact confirmation, got:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(tmpHome, ".xit", "kimi-hooks", "config.json"))
	if !strings.Contains(string(data), `"compact"`) {
		t.Errorf("expected compact in config, got:\n%s", string(data))
	}
}

func TestKimiHookStatusStyleDetailed(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "hook", "status-style", "kimi", "detailed", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status-style failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "detailed") {
		t.Errorf("expected detailed confirmation, got:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(tmpHome, ".xit", "kimi-hooks", "config.json"))
	if !strings.Contains(string(data), `"detailed"`) {
		t.Errorf("expected detailed in config, got:\n%s", string(data))
	}
}

func TestKimiHookStatusShowsRerouteNotice(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "status", "kimi", "--scope", "user")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "reroute_notice:") {
		t.Errorf("expected reroute_notice in status, got:\n%s", out)
	}
	if !strings.Contains(string(out), "notice_style:") {
		t.Errorf("expected notice_style in status, got:\n%s", out)
	}
	if !strings.Contains(string(out), "persistent_status_bar: not implemented") {
		t.Errorf("expected persistent_status_bar: not implemented in status, got:\n%s", out)
	}
}

func TestKimiHookDetailedReason(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "enable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "status-style", "kimi", "detailed", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	payload := `{"tool_name":"Shell","tool_input":{"command":"git diff"}}`
	cmd = exec.Command(bin, "kimi-hook", "observe")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-hook observe failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT intercepted") {
		t.Errorf("expected detailed 'XiT intercepted' reason, got:\n%s", out)
	}
	if !strings.Contains(string(out), "Recommended rerun") {
		t.Errorf("expected detailed 'Recommended rerun' reason, got:\n%s", out)
	}
}

func TestKimiHookCompactReason(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	cmd = exec.Command(bin, "hook", "enable-reroute", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Default style is compact.
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	cmd = exec.Command(bin, "kimi-hook", "observe")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-hook observe failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "deny") {
		t.Errorf("expected compact deny, got:\n%s", out)
	}
	if strings.Contains(string(out), "XiT intercepted") {
		t.Errorf("compact style should not contain 'XiT intercepted', got:\n%s", out)
	}
}

func TestKimiHookInstallCreatesFourLifecycleScripts(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}

	for _, name := range []string{"kimi-turn-userpromptsubmit.sh", "kimi-turn-stop.sh", "kimi-turn-sessionstart.sh", "kimi-turn-sessionend.sh"} {
		path := filepath.Join(tmpHome, ".xit", "hooks", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected script %s to exist", path)
		}
		data, _ := os.ReadFile(path)
		content := string(data)
		if !strings.Contains(content, "exec xit kimi-hook turn") {
			t.Errorf("expected script %s to contain 'exec xit kimi-hook turn', got:\n%s", name, content)
		}
	}

	// Verify hook status shows turn_scripts exist/executable.
	cmd = exec.Command(bin, "hook", "status", "kimi", "--scope", "user")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "turn_scripts:") {
		t.Errorf("expected turn_scripts in status, got:\n%s", out)
	}
	if !strings.Contains(string(out), "exists/executable") {
		t.Errorf("expected exists/executable in status, got:\n%s", out)
	}
}

func TestKimiHookUninstallRemovesOnlyXiTLifecycleHooks(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	os.WriteFile(filepath.Join(shimDir, "kimi"), []byte("#!/bin/sh\necho fake kimi"), 0755)

	// Install XiT hooks.
	cmd := exec.Command(bin, "init", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Add a non-XiT hook to config.
	configPath := filepath.Join(tmpHome, ".kimi", "config.toml")
	data, _ := os.ReadFile(configPath)
	os.WriteFile(configPath, []byte(string(data)+"\n[[hooks]]\nevent = \"UserPromptSubmit\"\ncommand = \"/usr/bin/other-hook.sh\"\n"), 0644)

	// Uninstall XiT.
	cmd = exec.Command(bin, "uninstall", "kimi", "--method", "official_hook", "--scope", "user", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "uninstalled") {
		t.Errorf("expected uninstalled message, got:\n%s", out)
	}

	// Verify non-XiT hook remains.
	data, _ = os.ReadFile(configPath)
	if !strings.Contains(string(data), "other-hook.sh") {
		t.Errorf("expected non-XiT hook to remain, got:\n%s", string(data))
	}
	if strings.Contains(string(data), "kimi-pretooluse-shell.sh") {
		t.Errorf("expected XiT hooks removed, got:\n%s", string(data))
	}
}

func TestKimiTurnStatusProjectStateFirst(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(projectDir, ".xit", "state"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpHome, ".xit", "state"), 0755)

	// Write project turn state.
	os.WriteFile(filepath.Join(projectDir, ".xit", "state", "turn.json"), []byte(`{"status":"thinking","event":"UserPromptSubmit","started_at":"2026-05-30T00:00:00Z"}`), 0644)
	// Write user turn state.
	os.WriteFile(filepath.Join(tmpHome, ".xit", "state", "turn.json"), []byte(`{"status":"turn_completed","event":"Stop","started_at":"2026-05-29T00:00:00Z","finished_at":"2026-05-29T00:01:00Z"}`), 0644)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "source:             project") {
		t.Errorf("expected project source, got:\n%s", out)
	}
	if !strings.Contains(string(out), "status:      thinking") {
		t.Errorf("expected project thinking status, got:\n%s", out)
	}
}

func TestKimiTurnStatusFallbackToUserState(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(projectDir, 0755)
	_ = os.MkdirAll(filepath.Join(tmpHome, ".xit", "state"), 0755)

	// Only user turn state exists.
	os.WriteFile(filepath.Join(tmpHome, ".xit", "state", "turn.json"), []byte(`{"status":"turn_completed","event":"Stop","started_at":"2026-05-29T00:00:00Z","finished_at":"2026-05-29T00:01:00Z"}`), 0644)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "source:             user") {
		t.Errorf("expected user source, got:\n%s", out)
	}
	if !strings.Contains(string(out), "status:      turn_completed") {
		t.Errorf("expected user turn_completed status, got:\n%s", out)
	}
}

func TestKimiTurnDiagnoseExists(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "kimi", "turn-diagnose")
	cmd.Env = append(os.Environ(), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-diagnose failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Kimi Turn Diagnose") {
		t.Errorf("expected diagnose header, got:\n%s", out)
	}
}

func TestKimiTurnDiagnoseJSONOutput(t *testing.T) {
	bin := buildXit(t)
	cmd := exec.Command(bin, "kimi", "turn-diagnose", "--json")
	cmd.Env = append(os.Environ(), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-diagnose --json failed: %v\n%s", err, out)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, string(out))
	}
	if _, ok := result["project_state"]; !ok {
		t.Error("expected project_state in JSON")
	}
	if _, ok := result["diagnosis"]; !ok {
		t.Error("expected diagnosis in JSON")
	}
}

func TestKimiTurnDiagnoseDetectsStatePathMismatch(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpHome, ".xit", "kimi-hooks"), 0755)
	// Write events log with events but no project state.
	rec := `{"time":"2026-05-30T00:00:00Z","event":"UserPromptSubmit","status":"thinking","cwd":"/tmp","state_file":"/tmp/.xit/state/turn.json"}` + "\n"
	os.WriteFile(filepath.Join(tmpHome, ".xit", "kimi-hooks", "turn-events.jsonl"), []byte(rec), 0644)

	cmd := exec.Command(bin, "kimi", "turn-diagnose")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-diagnose failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "state path mismatch") {
		t.Errorf("expected state path mismatch diagnosis, got:\n%s", out)
	}
}

func TestKimiTurnDiagnoseDetectsEventIdentityLost(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpHome, ".xit", "kimi-hooks"), 0755)
	// Write events log with empty event name.
	rec := `{"time":"2026-05-30T00:00:00Z","event":"","status":"active","cwd":"/tmp","state_file":"/tmp/.xit/state/turn.json"}` + "\n"
	os.WriteFile(filepath.Join(tmpHome, ".xit", "kimi-hooks", "turn-events.jsonl"), []byte(rec), 0644)

	cmd := exec.Command(bin, "kimi", "turn-diagnose")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-diagnose failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "event identity lost") {
		t.Errorf("expected event identity lost diagnosis, got:\n%s", out)
	}
}

func TestKimiTurnHookExplicitArgOverridesEmptyJSON(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(projectDir, ".xit"), 0755)
	oldWd, _ := os.Getwd()
	os.Chdir(projectDir)
	defer os.Chdir(oldWd)

	cmd := exec.Command(bin, "kimi-hook", "turn", "UserPromptSubmit")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Stdin = strings.NewReader(`{"event":"","cwd":"/tmp","session_id":"test"}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi-hook turn failed: %v\n%s", err, out)
	}
	if string(out) != "{}\n" {
		t.Errorf("expected empty JSON, got: %s", out)
	}

	// Verify state was written with correct event.
	turnPath := filepath.Join(projectDir, ".xit", "state", "turn.json")
	data, _ := os.ReadFile(turnPath)
	var state struct {
		Event  string `json:"event"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(data, &state)
	if state.Event != "UserPromptSubmit" {
		t.Errorf("expected event UserPromptSubmit, got %s", state.Event)
	}
	if state.Status != "thinking" {
		t.Errorf("expected status thinking, got %s", state.Status)
	}
}

func TestKimiTurnStatusActiveShowsGuardian(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(projectDir, ".xit", "state"), 0755)
	os.WriteFile(filepath.Join(projectDir, ".xit", "state", "turn.json"), []byte(`{"status":"active","event":"","started_at":"2026-05-30T00:00:00Z"}`), 0644)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T神功 · 守护你的T") {
		t.Errorf("expected toolbar 守护你的T for active state, got:\n%s", out)
	}
}

func TestKimiTurnStatusReadyWhenNoState(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(projectDir, 0755)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T神功 · 准备就绪") {
		t.Errorf("expected toolbar 准备就绪 when no state, got:\n%s", out)
	}
}

func TestKimiTurnStatusOldStateIgnored(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(projectDir, ".xit", "state"), 0755)
	// Write an old turn state (> 60s finished_at).
	oldTime := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	os.WriteFile(filepath.Join(projectDir, ".xit", "state", "turn.json"), []byte(`{"status":"turn_completed","event":"Stop","started_at":"`+oldTime+`","finished_at":"`+oldTime+`"}`), 0644)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T神功 · 准备就绪") {
		t.Errorf("expected toolbar 准备就绪 when old turn_completed, got:\n%s", out)
	}
}

func TestKimiHitrate(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi", "hitrate", "--last", "10m")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hitrate failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "XiT Kimi Routing Hit Rate") {
		t.Errorf("expected hitrate header, got:\n%s", out)
	}
}

func TestKimiImpact(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectHome := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(projectHome, 0755)

	// Write history with savings.
	now := time.Now().Format(time.RFC3339)
	rec := `{"timestamp":"` + now + `","command":"go test -v ./...","exit_code":0,"raw_bytes":100000,"summary_bytes":1000,"estimated_reduction":0.99,"duration_ms":100,"filter":"test","confidence":"high","policy":"should_compress","raw_log":"/tmp/test.raw.log"}` + "\n"
	_ = os.WriteFile(filepath.Join(projectHome, "history.jsonl"), []byte(rec), 0644)

	cmd := exec.Command(bin, "kimi", "impact", "--kimi-context", "149k")
	cmd.Env = stripEnv(os.Environ(), "HOME")
	cmd.Env = append(cmd.Env, "HOME="+tmpHome, "XIT_HOME="+projectHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi impact failed: %v\n%s", err, out)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "kimi_context_tokens: 149000") {
		t.Errorf("expected parsed context, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "saved_tokens:") {
		t.Errorf("expected saved_tokens, got:\n%s", outStr)
	}
	// 99k saved bytes / 4 = ~24750 tokens. 24750/149000 = ~16.6% → moderate
	if !strings.Contains(outStr, "moderate") && !strings.Contains(outStr, "strong") && !strings.Contains(outStr, "weak") {
		t.Errorf("expected verdict, got:\n%s", outStr)
	}
}

func TestKimiImpactJSON(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi", "impact", "--kimi-context", "100k", "--json")
	cmd.Env = stripEnv(os.Environ(), "HOME")
	cmd.Env = append(cmd.Env, "HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("kimi impact --json failed: %v\n%s", err, out)
	}
	var result struct {
		KimContextTokens int `json:"kimi_context_tokens"`
		Impact           struct {
			Verdict string `json:"verdict"`
		} `json:"impact"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, string(out))
	}
	if result.KimContextTokens != 100000 {
		t.Errorf("kimi_context_tokens = %d, want 100000", result.KimContextTokens)
	}
}

func TestKimiTurnStatusSessionStartShowsReady(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(projectDir, ".xit", "state"), 0755)
	os.WriteFile(filepath.Join(projectDir, ".xit", "state", "turn.json"), []byte(`{"status":"session_started","event":"SessionStart","started_at":"2026-05-30T00:00:00Z"}`), 0644)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T神功 · 准备就绪") {
		t.Errorf("expected toolbar 准备就绪 for SessionStart state, got:\n%s", out)
	}
}

func TestKimiTurnStatusUserPromptSubmitShowsGuarding(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	projectDir := filepath.Join(tmpHome, "project")
	_ = os.MkdirAll(filepath.Join(projectDir, ".xit", "state"), 0755)
	os.WriteFile(filepath.Join(projectDir, ".xit", "state", "turn.json"), []byte(`{"status":"thinking","event":"UserPromptSubmit","started_at":"2026-05-30T00:00:00Z"}`), 0644)

	cmd := exec.Command(bin, "kimi", "turn-status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+filepath.Join(tmpHome, ".xit"), "XIT_NONINTERACTIVE=1")
	cmd.Dir = projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("turn-status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "吸T神功 · 守护你的T") {
		t.Errorf("expected toolbar 守护你的T for UserPromptSubmit state, got:\n%s", out)
	}
}

func TestKimiStatusPatchPreviewShowsRotationInterval(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi", "status-patch", "preview")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status-patch preview failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "rotation_interval: 5s") {
		t.Errorf("expected rotation_interval: 5s in preview, got:\n%s", out)
	}
}

func TestKimiStatusPatchStatusShowsRotationInterval(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi", "status-patch", "status")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status-patch status failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "rotation_interval: 5s") {
		t.Errorf("expected rotation_interval: 5s in status, got:\n%s", out)
	}
}

func TestKimiStatusPatchValidate(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi", "status-patch", "validate")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status-patch validate failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "py_compile") && !strings.Contains(string(out), "ok") && !strings.Contains(string(out), "valid") {
		t.Errorf("expected validation result, got:\n%s", out)
	}
}

func TestKimiBlocked(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "kimi")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected kimi wrapper to fail when not initialized")
	}
	if !strings.Contains(string(out), "xit init kimi") {
		t.Errorf("expected 'xit init kimi' hint, got:\n%s", out)
	}
}

func TestShimInstallTakeoverRefused(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	shimDir := filepath.Join(tmpHome, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	kimiPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(kimiPath, []byte("#!/bin/sh\necho original kimi"), 0755)

	cmd := exec.Command(bin, "init", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	cmd.CombinedOutput()

	// Without --takeover should fail
	cmd = exec.Command(bin, "shim", "install", "kimi", "--yes")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "PATH="+shimDir, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("shim install without takeover should fail when original==shim")
	}
	if !strings.Contains(string(out), "--takeover") {
		t.Errorf("expected --takeover hint, got:\n%s", out)
	}
}

func TestClaudeStatuslineNoDaiGuanCe(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "claude", "statusline")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline failed: %v\n%s", err, out)
	}
	line := string(out)
	if strings.Contains(line, "待观测") {
		t.Errorf("statusLine should not contain 待观测, got: %s", line)
	}
}

func TestClaudeStatuslineNoColor(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "claude", "statusline")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1", "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline failed: %v\n%s", err, out)
	}
	line := string(out)
	if strings.Contains(line, "\033[") {
		t.Errorf("NO_COLOR should not emit ANSI codes, got: %q", line)
	}
	if !strings.Contains(line, "准备就绪") {
		t.Errorf("expected 准备就绪 in fallback, got: %s", line)
	}
}

func TestClaudeStatuslineJSON(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "claude", "statusline", "--json")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME=", "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline --json failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if data["line"] == nil {
		t.Error("missing line in JSON")
	}
	if data["color"] != "gold" {
		t.Errorf("expected color gold, got %v", data["color"])
	}
}

func TestClaudeStatuslineAutostateRunning(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	state := `{"status":"running","started_at":"` + time.Now().Format(time.RFC3339) + `","command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current.json"), []byte(state), 0644)

	cmd := exec.Command(bin, "claude", "statusline", "--json")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+tmpProject, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline --json failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	line, _ := data["line"].(string)
	if !strings.Contains(line, "正在吸T中") {
		t.Errorf("expected 正在吸T中 for running autostate, got: %s", line)
	}
	if data["source"] != "autostate_running" {
		t.Errorf("expected source autostate_running, got %v", data["source"])
	}
}

func TestClaudeStatuslineAutostateCompleted(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	state := `{"status":"completed","finished_at":"` + time.Now().Format(time.RFC3339) + `","saved_bytes":4000,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current.json"), []byte(state), 0644)

	cmd := exec.Command(bin, "claude", "statusline", "--json")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+tmpProject, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline --json failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	line, _ := data["line"].(string)
	if !strings.Contains(line, "本次省1k Token") {
		t.Errorf("expected 本次省1k Token for completed autostate, got: %s", line)
	}
	if data["source"] != "autostate_completed" {
		t.Errorf("expected source autostate_completed, got %v", data["source"])
	}
}

func TestGainTextOutput(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	historyPath := filepath.Join(tmpHome, "history.jsonl")
	lines := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","command":"go test -v ./...","exit_code":0,"raw_bytes":10000,"summary_bytes":500,"estimated_reduction":0.95,"duration_ms":100,"filter":"test","confidence":"high","raw_log":".xit/runs/1.raw.log"}`,
		`{"timestamp":"2026-01-01T00:01:00Z","command":"git status","exit_code":0,"raw_bytes":200,"summary_bytes":180,"estimated_reduction":0.1,"duration_ms":10,"filter":"git","confidence":"high","raw_log":".xit/runs/2.raw.log"}`,
	}
	_ = os.WriteFile(historyPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	cmd := exec.Command(bin, "gain")
	cmd.Env = append(os.Environ(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit gain failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "XiT Gain Report") {
		t.Errorf("expected XiT Gain Report, got:\n%s", s)
	}
	if !strings.Contains(s, "Total commands condensed: 2") {
		t.Errorf("expected Total commands condensed: 2, got:\n%s", s)
	}
}

func TestGainJSONOutput(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	historyPath := filepath.Join(tmpHome, "history.jsonl")
	lines := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","command":"go test -v ./...","exit_code":0,"raw_bytes":10000,"summary_bytes":500,"estimated_reduction":0.95,"duration_ms":100,"filter":"test","confidence":"high","raw_log":".xit/runs/1.raw.log"}`,
		`{"timestamp":"2026-01-01T00:01:00Z","command":"git status","exit_code":0,"raw_bytes":200,"summary_bytes":180,"estimated_reduction":0.1,"duration_ms":10,"filter":"git","confidence":"high","raw_log":".xit/runs/2.raw.log"}`,
	}
	_ = os.WriteFile(historyPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	cmd := exec.Command(bin, "gain", "--json")
	cmd.Env = append(os.Environ(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit gain --json failed: %v\n%s", err, out)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}

	if data["total_commands_condensed"] != float64(2) {
		t.Errorf("expected total_commands_condensed=2, got %v", data["total_commands_condensed"])
	}
	if data["raw_bytes"] != float64(10200) {
		t.Errorf("expected raw_bytes=10200, got %v", data["raw_bytes"])
	}
	if data["summary_bytes"] != float64(680) {
		t.Errorf("expected summary_bytes=680, got %v", data["summary_bytes"])
	}
	if data["saved_bytes"] != float64(9520) {
		t.Errorf("expected saved_bytes=9520, got %v", data["saved_bytes"])
	}
	if data["saved_tokens"] != float64(2380) {
		t.Errorf("expected saved_tokens=2380, got %v", data["saved_tokens"])
	}
	if data["estimated_reduction"] == nil {
		t.Error("expected estimated_reduction")
	}

	top, ok := data["top_commands"].([]interface{})
	if !ok || len(top) == 0 {
		t.Fatalf("expected top_commands, got %v", data["top_commands"])
	}
	first := top[0].(map[string]interface{})
	if first["command"] != "go test -v ./..." {
		t.Errorf("expected top command go test -v ./..., got %v", first["command"])
	}
	if first["runs"] != float64(1) {
		t.Errorf("expected runs=1, got %v", first["runs"])
	}

	s := string(out)
	if strings.Contains(s, "\x1b[") {
		t.Errorf("JSON output should not contain ANSI escape codes")
	}
}

func TestGainJSONNoHistory(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "gain", "--json")
	cmd.Env = append(os.Environ(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit gain --json with no history failed: %v\n%s", err, out)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON for empty history: %v\n%s", err, out)
	}
	if data["total_commands_condensed"] != float64(0) {
		t.Errorf("expected 0 commands for empty history, got %v", data["total_commands_condensed"])
	}
	if data["raw_bytes"] != float64(0) {
		t.Errorf("expected 0 raw_bytes for empty history, got %v", data["raw_bytes"])
	}
}

func TestGainJSONMalformedLine(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	historyPath := filepath.Join(tmpHome, "history.jsonl")
	content := "not json at all\n" +
		`{"timestamp":"2026-01-01T00:00:00Z","command":"go test -v ./...","exit_code":0,"raw_bytes":1000,"summary_bytes":100,"estimated_reduction":0.9,"duration_ms":10,"filter":"test","confidence":"high","raw_log":".xit/runs/1.raw.log"}` +
		"\n"
	_ = os.WriteFile(historyPath, []byte(content), 0644)

	cmd := exec.Command(bin, "gain", "--json")
	cmd.Env = append(os.Environ(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit gain --json with malformed line failed: %v\n%s", err, out)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if data["total_commands_condensed"] != float64(1) {
		t.Errorf("expected 1 valid command, got %v", data["total_commands_condensed"])
	}
	warnings, ok := data["warnings"].([]interface{})
	if !ok || len(warnings) == 0 {
		t.Errorf("expected warnings for malformed line, got %v", data["warnings"])
	}
}

// TestAutoOpencodeOutputsFourLines verifies that when XIT_ADAPTER=opencode is set,
// xit auto emits the four-line Chinese brand output instead of the English summary.
func TestAutoOpencodeOutputsFourLines(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	// Fake git that produces high-noise output (>100 lines triggers compression).
	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\nfor i in $(seq 1 200); do echo \"+ line $i changed\"; done"), 0755)

	cmd := exec.Command(bin, "auto", "git", "diff")
	cmd.Env = append(os.Environ(),
		"PATH="+tmpPath,
		"XIT_ORIGINAL_GIT="+gitPath,
		"XIT_HOME=",
		"XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=opencode",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto git diff (opencode) failed: %v\n%s", err, out)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "吸T神功 · 守护你的T") {
		t.Errorf("expected 吸T神功 · 守护你的T, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "吸T神功 · 本次已发功") {
		t.Errorf("expected 吸T神功 · 本次已发功, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "本次省") {
		t.Errorf("expected 本次省 token line, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "吸T神功 · 等待下轮发功") {
		t.Errorf("expected 吸T神功 · 等待下轮发功, got:\n%s", outStr)
	}
	// Must NOT contain the English summary header or old per-session count.
	if strings.Contains(outStr, "吸T完成") {
		t.Errorf("should not contain 吸T完成 in opencode mode, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "本轮共吸") {
		t.Errorf("should not contain 本轮共吸 (cross-turn count removed), got:\n%s", outStr)
	}
}

// TestAutoOpencodeEnvNotLeakedToChild verifies that XIT_ADAPTER and
// XIT_OPENCODE_REROUTE_COUNT are stripped from the child process environment.
func TestAutoOpencodeEnvNotLeakedToChild(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	// Fake "env" binary that prints XIT_ADAPTER from its own environment.
	envScript := filepath.Join(tmpPath, "env")
	os.WriteFile(envScript, []byte("#!/bin/sh\nprintenv XIT_ADAPTER; printenv XIT_OPENCODE_REROUTE_COUNT; exit 0"), 0755)

	cmd := exec.Command(bin, "auto", "env")
	cmd.Env = append(os.Environ(),
		"PATH="+tmpPath,
		"XIT_ORIGINAL_ENV="+envScript,
		"XIT_HOME=",
		"XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=opencode",
		"XIT_OPENCODE_REROUTE_COUNT=3",
	)
	out, err := cmd.CombinedOutput()
	// env exits 0, xit auto may exit 0 too (passthrough for small output).
	_ = err
	outStr := string(out)
	// The child env output must NOT contain XIT_ADAPTER=opencode.
	if strings.Contains(outStr, "XIT_ADAPTER") {
		t.Errorf("XIT_ADAPTER leaked into child process env, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "XIT_OPENCODE_REROUTE_COUNT") {
		t.Errorf("XIT_OPENCODE_REROUTE_COUNT leaked into child process env, got:\n%s", outStr)
	}
}
