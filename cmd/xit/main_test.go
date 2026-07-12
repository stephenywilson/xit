package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stephenywilson/xit/internal/opencodehook"
	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/shim"
	"github.com/stephenywilson/xit/internal/vscodebridge"
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

// seedBlockedVersionCache writes a fresh ~/.xit/version-check.json under the
// given fake HOME that declares the running CLI below-minimum, so the version
// gate (which reads cache only on the hot path) evaluates to severity=blocked
// without any network access.
func seedBlockedVersionCache(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".xit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := map[string]any{
		"latest_cli":  "99.0.0",
		"min_cli":     "99.0.0", // current version < min => escalates to blocked
		"severity":    "blocked",
		"message":     "This XiT version is no longer supported.",
		"npm_command": "npm install -g xitsg@latest",
		"fetched_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, _ := json.Marshal(body)
	if err := os.WriteFile(filepath.Join(dir, "version-check.json"), data, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

// TestVersionBlockedRefusesAuto: severity=blocked must refuse the core
// `xit auto` path (spec §六) — non-zero exit, with the upgrade command
// printed and the user's command NEVER run. Fail-closed only for the core
// path; never auto-installs.
func TestVersionBlockedRefusesAuto(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	seedBlockedVersionCache(t, home)

	marker := filepath.Join(t.TempDir(), "ran.txt")
	cmd := exec.Command(bin, "auto", "sh", "-c", "echo hi > "+marker)
	cmd.Env = append(noXitAdapterEnv(), "HOME="+home, "XIT_NONINTERACTIVE=1")
	cmd.Env = stripEnv(cmd.Env, "XIT_API_BASE")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("blocked `xit auto` must exit non-zero; got success\n%s", out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("blocked `xit auto` must NOT run the user's command")
	}
	if !strings.Contains(string(out), "npm install -g xitsg@latest") {
		t.Fatalf("blocked output must show the upgrade command, got:\n%s", out)
	}
}

// TestVersionBlockedNeverBlocksRecoveryCommands: even when blocked,
// --version / doctor / telemetry / upgrade must keep working (spec §六.1) —
// they are the user's only way to recover, and are dispatched before the gate.
func TestVersionBlockedNeverBlocksRecoveryCommands(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	seedBlockedVersionCache(t, home)

	for _, args := range [][]string{
		{"--version"},
		{"telemetry", "status"},
		{"doctor"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(noXitAdapterEnv(), "HOME="+home, "XIT_NONINTERACTIVE=1")
		cmd.Env = stripEnv(cmd.Env, "XIT_API_BASE")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("`xit %s` must NOT be blocked by version gate: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// noXitAdapterEnv returns os.Environ() with XiT adapter env stripped so tests
// are not affected by an outer shell running under an adapter.
func noXitAdapterEnv() []string {
	env := stripEnv(os.Environ(), "XIT_ADAPTER")
	env = stripEnv(env, "XIT_OPENCODE_REROUTE_COUNT")
	env = stripEnv(env, "XIT_OPENCODE_TURN_KEY")
	env = stripEnv(env, "XIT_OPENCODE_SESSION_ID")
	env = stripEnv(env, "XIT_OPENCODE_USER_MESSAGE_ID")
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

	// internal/telemetry and internal/updatecheck are the ONLY two packages
	// allowed to make network calls — anonymous metrics ingest and the version
	// check, both documented (docs/telemetry.md), opt-out, and fail-open. They
	// are excluded here so the guard stays strict for every OTHER file (no
	// other code path may ever reach the network), without false-positiving on
	// the sanctioned egress points.
	sanctioned := []string{
		filepath.FromSlash("internal/telemetry/"),
		filepath.FromSlash("internal/updatecheck/"),
	}

	root := "../../"
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, s := range sanctioned {
			if strings.Contains(filepath.ToSlash(path), filepath.ToSlash(s)) {
				return nil
			}
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
	// Assert against the actual `version` const so this never drifts on a bump.
	if !strings.Contains(string(out), version) {
		t.Errorf("expected version %s, got: %s", version, out)
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

// parsePercentAfter extracts the integer percent that immediately follows label
// (e.g. "压缩率: ") and precedes the next '%'.
func parsePercentAfter(t *testing.T, out, label string) int {
	t.Helper()
	idx := strings.Index(out, label)
	if idx < 0 {
		t.Fatalf("label %q not found in:\n%s", label, out)
	}
	rest := out[idx+len(label):]
	pctIdx := strings.Index(rest, "%")
	if pctIdx < 0 {
		t.Fatalf("no %% after %q in:\n%s", label, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:pctIdx]))
	if err != nil {
		t.Fatalf("could not parse percent from %q: %v", rest[:pctIdx], err)
	}
	return n
}

// TestFormatTokenHuman locks the global user-visible token format:
// >=1000 -> "约 X.XXk Token" (2 decimals), <1000 -> "N Token".
func TestFormatTokenHuman(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0 Token"},
		{293, "293 Token"},
		{999, "999 Token"},
		{1000, "约 1.00k Token"},
		{10291, "约 10.29k Token"},
		{15234, "约 15.23k Token"},
	}
	for _, c := range cases {
		if got := formatTokenHuman(c.n); got != c.want {
			t.Errorf("formatTokenHuman(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestFormatTokenAntigravity locks the Antigravity-only token format: two-decimal
// k WITHOUT the "约" prefix.
func TestFormatTokenAntigravity(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{999, "999 Token"},
		{1000, "1.00k Token"},
		{10283, "10.28k Token"},
		{10291, "10.29k Token"},
		{15234, "15.23k Token"},
	}
	for _, c := range cases {
		got := formatTokenAntigravity(c.n)
		if got != c.want {
			t.Errorf("formatTokenAntigravity(%d) = %q, want %q", c.n, got, c.want)
		}
		if strings.Contains(got, "约") {
			t.Errorf("formatTokenAntigravity(%d) must not contain 约, got %q", c.n, got)
		}
	}
}

// TestEffectiveAdapter verifies adapter resolution: explicit XIT_ADAPTER wins,
// otherwise Antigravity is detected from the process ancestor chain (mocked via
// XIT_TEST_ANCESTORS), and unrelated shells resolve to "".
func TestEffectiveAdapter(t *testing.T) {
	for _, k := range []string{"XIT_ADAPTER", "XIT_TEST_ANCESTORS", "XIT_TEST_CLAUDECODE"} {
		orig, had := os.LookupEnv(k)
		key := k
		t.Cleanup(func() {
			if had {
				os.Setenv(key, orig)
			} else {
				os.Unsetenv(key)
			}
		})
	}

	cases := []struct {
		name, adapter, ancestors, claudecode, want string
	}{
		{"explicit antigravity", "antigravity", "bash", "0", "antigravity"},
		{"explicit opencode wins over ancestry+claudecode", "opencode", "agy", "1", "opencode"},
		{"ancestor agy", "", "agy,bash,zsh", "0", "antigravity"},
		{"ancestor antigravity-cli", "", "/usr/local/bin/antigravity-cli,bash", "0", "antigravity"},
		{"ancestor antigravity path gemini", "", "/Users/x/.gemini/antigravity-cli/bin/gemini,zsh", "0", "antigravity"},
		{"antigravity ancestry wins over claudecode", "", "agy,bash", "1", "antigravity"},
		{"antigravity ancestry wins over claude ancestor", "", "agy,claude", "0", "antigravity"},
		{"claudecode env => claude", "", "bash,zsh", "1", "claude"},
		{"ancestor claude basename => claude", "", "/Users/x/.vscode/extensions/anthropic.claude-code-2.1.183-darwin-arm64/resources/native-binary/claude,bash", "0", "claude"},
		{"ancestor claude-code basename => claude", "", "/usr/local/bin/claude-code,zsh", "0", "claude"},
		{"plain shell no claudecode", "", "bash,zsh,login,Terminal", "0", ""},
		{"legacy must not match agy", "", "legacy,bash", "0", ""},
		{"bare gemini must not match", "", "gemini,bash", "0", ""},
		{"node ancestor must not match claude", "", "node,bash", "0", ""},
		{"claude-flow must not substring-match claude", "", "claude-flow,bash", "0", ""},
		{"dir containing claude must not match", "", "/Users/claude-fan/bin/myapp,bash", "0", ""},
	}
	for _, c := range cases {
		if c.adapter == "" {
			os.Unsetenv("XIT_ADAPTER")
		} else {
			os.Setenv("XIT_ADAPTER", c.adapter)
		}
		os.Setenv("XIT_TEST_ANCESTORS", c.ancestors)
		os.Setenv("XIT_TEST_CLAUDECODE", c.claudecode)
		if got := effectiveAdapter(); got != c.want {
			t.Errorf("%s: effectiveAdapter()=%q want %q", c.name, got, c.want)
		}
	}
}

// TestAutoSummaryHumanTokenFormat verifies the CLI summary uses the new "约 X.XXk
// Token" format (not the lossy "10k") and shows 压缩率 not 降噪率.
func TestAutoSummaryHumanTokenFormat(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 640 ]; do echo \"block line $i hello xit compress aaaa bbbb cccc dddd eeee ffff\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "本次节省: 约 ") || !strings.Contains(s, "k Token") {
		t.Errorf("expected '本次节省: 约 X.XXk Token', got:\n%s", s)
	}
	if !strings.Contains(s, "压缩率") || strings.Contains(s, "降噪率") {
		t.Errorf("expected 压缩率 and no 降噪率, got:\n%s", s)
	}
	// The old lossy "10k Token" form has no "约 " prefix; the new form always does.
	if regexp.MustCompile(`[0-9]+k Token`).MatchString(s) && !strings.Contains(s, "约 ") {
		t.Errorf("must not contain lossy 'Nk Token' form, got:\n%s", s)
	}
}

// TestAutoOpencodeToolOutputShowsTwoLineFooter verifies OpenCode tool cards show
// the fixed two-line XiT footer while hiding machine fields.
func TestAutoOpencodeToolOutputShowsTwoLineFooter(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 640 ]; do echo \"block line $i hello xit compress aaaa bbbb cccc dddd eeee ffff\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=opencode", "XIT_OPENCODE_TURN_KEY=turn-format")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (opencode) failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "吸T神功 · OpenCode · 守护你的T") ||
		!strings.Contains(s, "本次省 约 ") ||
		!strings.Contains(s, "k Token · 本轮共吸 1次") {
		t.Errorf("expected OpenCode two-line footer, got:\n%s", s)
	}
	if strings.Contains(s, "命令执行成功，无需展开重复输出。") {
		t.Errorf("OpenCode pure success should not include generic success line, got:\n%s", s)
	}
	for _, bad := range []string{"command:", "exit_code:", "raw_log:", "key_facts:", "降噪率", "压缩率", "saved_tokens:", ".xit/runs"} {
		if strings.Contains(s, bad) {
			t.Errorf("opencode output must not contain %q, got:\n%s", bad, s)
		}
	}
	st := opencodehook.ReadTurnStateByKey(tmpHome, "turn-format")
	if st == nil || st.RunCount != 1 || st.SavedTokensTotal <= 0 {
		t.Fatalf("expected pending OpenCode turn state, got %+v", st)
	}
}

// TestAutoSummaryShowsRealCompressionRate verifies the human summary prints a
// real 压缩率 (savedBytes/rawBytes), not the filter's EstimatedReduction.
// "noisycmd" is unknown -> routed to the fallback filter whose EstimatedReduction
// is hardcoded 0.0; this is the exact case that used to print the contradictory
// "本次节省 1k / 降噪率 0%". 压缩率 must be > 0 and equal saved/raw from run state.
func TestAutoSummaryShowsRealCompressionRate(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compression rate test\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "吸T完成") {
		t.Fatalf("expected compressed summary (吸T完成), got:\n%s", s)
	}
	if !strings.Contains(s, "本次节省") {
		t.Errorf("expected 本次节省, got:\n%s", s)
	}
	if !strings.Contains(s, "压缩率") {
		t.Errorf("expected 压缩率, got:\n%s", s)
	}
	if strings.Contains(s, "降噪率") {
		t.Errorf("must NOT contain 降噪率 (replaced by real 压缩率), got:\n%s", s)
	}
	rate := parsePercentAfter(t, s, "压缩率: ")
	if rate <= 0 {
		t.Errorf("压缩率 must be > 0 when 本次节省 > 0 (no 0%% contradiction), got %d%%\n%s", rate, s)
	}

	// 压缩率 must equal savedBytes/rawBytes from the real run state (same source as 本次节省).
	statePath := filepath.Join(tmpHome, "state", "current-run.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("expected state file at %s: %v", statePath, err)
	}
	var st struct {
		RawBytes     int `json:"raw_bytes"`
		SummaryBytes int `json:"summary_bytes"`
		SavedBytes   int `json:"saved_bytes"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	if st.RawBytes <= 0 {
		t.Fatalf("expected raw_bytes > 0 in state, got %d", st.RawBytes)
	}
	expected := int(float64(st.SavedBytes)/float64(st.RawBytes)*100 + 0.5)
	if diff := rate - expected; diff < -1 || diff > 1 {
		t.Errorf("压缩率 %d%% != savedBytes/rawBytes %d%% (saved=%d raw=%d) — must be same source as 本次节省",
			rate, expected, st.SavedBytes, st.RawBytes)
	}
}

// TestAutoSummaryLargeOutputHighCompressionRate verifies large repetitive output
// yields a real, high 压缩率 (not 0, not the filter estimate).
func TestAutoSummaryLargeOutputHighCompressionRate(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 3000 ]; do echo \"line $i hello xit compression rate test\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (large) failed: %v\n%s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "降噪率") {
		t.Errorf("must NOT contain 降噪率, got:\n%s", s)
	}
	rate := parsePercentAfter(t, s, "压缩率: ")
	if rate < 50 {
		t.Errorf("large repetitive output should compress heavily; 压缩率 %d%% too low\n%s", rate, s)
	}
}

// TestAutoPassthroughNoCompressionMetric verifies small passthrough output shows
// neither 压缩率 nor 降噪率 (no fake compression metric for un-summarized output).
func TestAutoPassthroughNoCompressionMetric(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\necho 'small output'"), 0755)

	cmd := exec.Command(bin, "auto", "git", "status")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_ORIGINAL_GIT="+gitPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto git status failed: %v\n%s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "压缩率") || strings.Contains(s, "降噪率") {
		t.Errorf("passthrough small output must not show a compression metric, got:\n%s", s)
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
	// Isolate XIT_HOME to an empty temp dir so the statusline reads no real run
	// data. With XIT_HOME unset, xitHome() falls back to <cwd>/.xit — and during
	// `go test` cwd is cmd/xit, whose dogfood .xit/history.jsonl would otherwise
	// make this fallback test read a real "本次省NNk Token" line.
	tmpProject := t.TempDir()
	cmd := exec.Command(bin, "claude", "statusline")
	// No active run -> the merged idle line (contains 准备就绪) is poll-safe regardless of call count.
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+tmpProject, "XIT_NONINTERACTIVE=1", "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline failed: %v\n%s", err, out)
	}
	line := string(out)
	if strings.Contains(line, "\033[") {
		t.Errorf("NO_COLOR should not emit ANSI codes, got: %q", line)
	}
	if !strings.Contains(line, "准备就绪") {
		t.Errorf("expected 准备就绪 on first idle call, got: %s", line)
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
	state := `{"status":"running","adapter":"claude","started_at":"` + time.Now().Format(time.RFC3339) + `","command":"go test"}`
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
	state := `{"status":"completed","adapter":"claude","finished_at":"` + time.Now().Format(time.RFC3339) + `","completed_at":"` + time.Now().Format(time.RFC3339) + `","saved_bytes":4000,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current.json"), []byte(state), 0644)

	cmd := exec.Command(bin, "claude", "statusline", "--json")
	// No CLAUDE_CODE_SESSION_ID/count -> no 本轮共吸 suffix expected either.
	cmd.Env = append(stripEnv(os.Environ(), "CLAUDE_CODE_SESSION_ID"), "HOME="+tmpHome, "XIT_HOME="+tmpProject, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit claude statusline --json failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	line, _ := data["line"].(string)
	if !strings.Contains(line, "吸T完成 · Claude · 本轮省 1.00k Token") {
		t.Errorf("expected '吸T完成 · Claude · 本轮省 1.00k Token' for completed autostate, got: %s", line)
	}
	if strings.Contains(line, "命中率") || strings.Contains(line, "本次省") || strings.Contains(line, "约") {
		t.Errorf("Claude completed must not contain 命中率/本次省/约, got: %s", line)
	}
	if data["source"] != "autostate_completed" {
		t.Errorf("expected source autostate_completed, got %v", data["source"])
	}
}

// antigravityStatuslineJSON runs `xit antigravity statusline --json` with an
// isolated XIT_HOME and returns the parsed payload.
func antigravityStatuslineJSON(t *testing.T, tmpProject string) map[string]interface{} {
	t.Helper()
	bin := buildXit(t)
	tmpHome := t.TempDir()
	cmd := exec.Command(bin, "antigravity", "statusline", "--json")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+tmpProject, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("xit antigravity statusline --json failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	return data
}

// TestAntigravityStatuslineRunning verifies a fresh running current-run drives
// the "Antigravity · 正在吸T中" lifecycle line (the bug: it used to show 准备就绪).
func TestAntigravityStatuslineRunning(t *testing.T) {
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	state := `{"status":"running","adapter":"antigravity","started_at":"` + time.Now().Format(time.RFC3339) + `","heartbeat_at":"` + time.Now().Format(time.RFC3339) + `","command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	if !strings.Contains(line, "Antigravity · 正在吸T中") {
		t.Errorf("expected 'Antigravity · 正在吸T中' for running autostate, got: %s", line)
	}
	if data["source"] != "autostate_running" {
		t.Errorf("expected source autostate_running, got %v", data["source"])
	}
}

// TestAntigravityStatuslineCompletedSettle verifies that an explicit "settling"
// state shows "正在收功中" (correct character 功, not 工) and not the saved-token
// line.
func TestAntigravityStatuslineCompletedSettle(t *testing.T) {
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	nowStr := time.Now().Format(time.RFC3339)
	state := `{"status":"settling","adapter":"antigravity","started_at":"` + nowStr + `","heartbeat_at":"` + nowStr + `","saved_bytes":4000,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	if !strings.Contains(line, "吸T完成 · Antigravity · 正在收功中") {
		t.Errorf("expected '吸T完成 · Antigravity · 正在收功中' for settling state, got: %s", line)
	}
	if strings.Contains(line, "正在收工中") || strings.Contains(line, "结果整理中") {
		t.Errorf("must NOT use typo 正在收工中 / old 结果整理中, got: %s", line)
	}
	if strings.Contains(line, "本轮省") || strings.Contains(line, "本次省") {
		t.Errorf("must NOT show saved tokens during settling, got: %s", line)
	}
	if data["source"] != "autostate_settling" {
		t.Errorf("expected source autostate_settling, got %v", data["source"])
	}
}

// TestAntigravityStatuslineStaleSettlingFallback verifies a stale settling state
// (heartbeat older than the freshness window) does NOT stick on 正在收功中.
func TestAntigravityStatuslineStaleSettlingFallback(t *testing.T) {
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	old := time.Now().Add(-30 * time.Second).Format(time.RFC3339)
	state := `{"status":"settling","adapter":"antigravity","started_at":"` + old + `","heartbeat_at":"` + old + `","saved_bytes":4000,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	if strings.Contains(line, "正在收功中") {
		t.Errorf("stale settling must NOT keep showing 正在收功中, got: %s", line)
	}
}

// TestAntigravityStatuslineCompletedSavedAfterSettle verifies that once the settle
// window has passed, the completed window STABLY shows the real saved-token line
// (poll-safe: no rotation), and never the low-info "本次已发功" or settle text.
func TestAntigravityStatuslineCompletedSavedAfterSettle(t *testing.T) {
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	past := time.Now().Add(-10 * time.Second).Format(time.RFC3339) // past 1s settle, within 30s fresh
	state := `{"status":"completed","adapter":"antigravity","finished_at":"` + past + `","completed_at":"` + past + `","saved_bytes":4000,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	if !strings.Contains(line, "吸T完成 · Antigravity · 本轮省 1.00k Token") {
		t.Errorf("expected stable '吸T完成 · Antigravity · 本轮省 1.00k Token', got: %s", line)
	}
	if strings.Contains(line, "约") {
		t.Errorf("Antigravity completed must NOT contain 约, got: %s", line)
	}
	for _, bad := range []string{"本次已发功", "本轮共吸", "等待下轮发功", "本次省", "正在收功中", "正在收工中"} {
		if strings.Contains(line, bad) {
			t.Errorf("completed line must NOT contain %q, got: %s", bad, line)
		}
	}
	if data["source"] != "autostate_completed" {
		t.Errorf("expected source autostate_completed, got %v", data["source"])
	}
}

// TestAntigravityStatuslineWaiting verifies that after a recent run produced real
// savings (history within the window) but no fresh completed/running state, the
// statusline rotates into the waiting flow (等待下轮发功 / 守护你的T), not 准备就绪.
// TestAntigravityStatuslineNoCompletedLowInfoRotation verifies that across the
// whole completed window (1s–30s) and repeated calls, the line is always the
// stable saved-token line and never the low-info "本次已发功" — so a host that
// samples the statusline only once cannot get stuck on a low-value line.
func TestAntigravityStatuslineNoCompletedLowInfoRotation(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	for _, ageSec := range []int{2, 5, 10, 20, 28} {
		tmpProject := t.TempDir()
		_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
		ts := time.Now().Add(-time.Duration(ageSec) * time.Second).Format(time.RFC3339)
		state := `{"status":"completed","adapter":"antigravity","finished_at":"` + ts + `","completed_at":"` + ts + `","saved_bytes":41167,"command":"go test"}`
		_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

		// Two back-to-back calls (different wall-clock instants) to defeat any
		// residual time-bucket rotation.
		for call := 0; call < 2; call++ {
			cmd := exec.Command(bin, "antigravity", "statusline", "--json")
			cmd.Env = append(os.Environ(), "HOME="+tmpHome, "XIT_HOME="+tmpProject, "XIT_NONINTERACTIVE=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("statusline failed (age=%ds): %v\n%s", ageSec, err, out)
			}
			var data map[string]interface{}
			if err := json.Unmarshal(out, &data); err != nil {
				t.Fatalf("invalid JSON (age=%ds): %v\n%s", ageSec, err, out)
			}
			line, _ := data["line"].(string)
			if strings.Contains(line, "本次已发功") {
				t.Errorf("age=%ds call=%d: completed window must never show 本次已发功, got: %s", ageSec, call, line)
			}
			if !strings.Contains(line, "吸T完成 · Antigravity · 本轮省 10.29k Token") {
				t.Errorf("age=%ds call=%d: expected stable saved-token line, got: %s", ageSec, call, line)
			}
			if strings.Contains(line, "约") {
				t.Errorf("age=%ds call=%d: Antigravity completed must NOT contain 约, got: %s", ageSec, call, line)
			}
		}
	}
}

// TestAntigravityStatuslineNoWaiting verifies the "等待下轮发功" state is gone:
// recent savings in history (but no fresh state) must NOT produce a waiting line.
func TestAntigravityStatuslineNoWaiting(t *testing.T) {
	tmpProject := t.TempDir()
	rec := `{"timestamp":"` + time.Now().Format(time.RFC3339) + `","command":"go test","exit_code":0,"raw_bytes":10000,"summary_bytes":500,"raw_log":"r.log"}` + "\n"
	_ = os.WriteFile(filepath.Join(tmpProject, "history.jsonl"), []byte(rec), 0644)

	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	for _, bad := range []string{"等待下轮发功", "本次已发功", "本轮共吸"} {
		if strings.Contains(line, bad) {
			t.Errorf("Antigravity must NOT show %q, got: %s", bad, line)
		}
	}
	// With no hook events, this is idle.
	if !strings.Contains(line, "吸T神功 · Antigravity · 准备就绪") {
		t.Errorf("expected idle 准备就绪 (no waiting state), got: %s", line)
	}
}

// TestAntigravityStatuslineIdle verifies the FIRST idle call of a session shows
// "准备就绪" (single, no rotation).
func TestAntigravityStatuslineIdle(t *testing.T) {
	tmpProject := t.TempDir()
	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	if !strings.Contains(line, "吸T神功 · Antigravity · 准备就绪") {
		t.Errorf("expected first-idle line 准备就绪, got: %s", line)
	}
	if data["source"] != "idle_ready" {
		t.Errorf("expected source idle_ready, got %v", data["source"])
	}
}

// agyIdle runs `xit antigravity statusline --json` with a fixed session key and
// returns (line, source).
func agyIdle(t *testing.T, bin, home, sessionKey string) (string, string) {
	t.Helper()
	cmd := exec.Command(bin, "antigravity", "statusline", "--json")
	cmd.Env = append(os.Environ(), "HOME="+home, "XIT_HOME="+home, "XIT_NONINTERACTIVE=1",
		"XIT_TEST_SESSION_KEY="+sessionKey)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	line, _ := data["line"].(string)
	source, _ := data["source"].(string)
	return line, source
}

// TestAntigravityStatuslineIdleFirstThenGuard verifies the session-scoped idle
// strategy: first call -> 准备就绪, later calls -> 守护你的T; a new/stale session
// resets to 准备就绪.
func TestAntigravityStatuslineIdleFirstThenGuard(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()

	// First idle call of session "s1".
	if l, s := agyIdle(t, bin, home, "s1"); !strings.Contains(l, "准备就绪") || s != "idle_ready" {
		t.Errorf("call 1 expected 准备就绪/idle_ready, got %q/%q", l, s)
	}
	// Second + third call of the SAME session -> 守护你的T.
	if l, s := agyIdle(t, bin, home, "s1"); !strings.Contains(l, "守护你的T") || s != "idle_guard" {
		t.Errorf("call 2 expected 守护你的T/idle_guard, got %q/%q", l, s)
	}
	if l, _ := agyIdle(t, bin, home, "s1"); !strings.Contains(l, "守护你的T") {
		t.Errorf("call 3 expected 守护你的T, got %q", l)
	}
	// A different session -> back to 准备就绪.
	if l, s := agyIdle(t, bin, home, "s2"); !strings.Contains(l, "准备就绪") || s != "idle_ready" {
		t.Errorf("new session expected 准备就绪/idle_ready, got %q/%q", l, s)
	}

	// Stale session resets to 准备就绪: backdate the recorded state for "s2".
	statePath := filepath.Join(home, "state", "antigravity-statusline.json")
	old := time.Now().Add(-30 * time.Minute).Format(time.RFC3339)
	_ = os.WriteFile(statePath, []byte(`{"session_key":"s2","idle_calls":5,"updated_at":"`+old+`"}`), 0644)
	if l, s := agyIdle(t, bin, home, "s2"); !strings.Contains(l, "准备就绪") || s != "idle_ready" {
		t.Errorf("stale session expected 准备就绪/idle_ready, got %q/%q", l, s)
	}
}

// TestAntigravityDefaultSettleDelayIs2s verifies the settle buffer default is 2.0s.
func TestAntigravityDefaultSettleDelayIs2s(t *testing.T) {
	orig, had := os.LookupEnv("XIT_ANTIGRAVITY_SETTLE_MS")
	os.Unsetenv("XIT_ANTIGRAVITY_SETTLE_MS")
	t.Cleanup(func() {
		if had {
			os.Setenv("XIT_ANTIGRAVITY_SETTLE_MS", orig)
		}
	})
	if got := antigravitySettleDelay(); got != 2*time.Second {
		t.Errorf("default antigravity settle delay = %v, want 2s", got)
	}
}

// TestAntigravityAutoOutputNaturalLanguage verifies XiT's `xit auto` stdout under
// XIT_ADAPTER=antigravity is a short natural-language result, hiding all machine
// bookkeeping fields and any raw_log / .xit/runs path.
func TestAntigravityAutoOutputNaturalLanguage(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=antigravity", "XIT_ANTIGRAVITY_SETTLE_MS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (antigravity) failed: %v\n%s", err, out)
	}
	s := string(out)
	for _, bad := range []string{
		"command:", "exit_code:", "status:", "reduction:", "saved_tokens:",
		"raw_log:", "key facts", "key_facts", ".xit/runs",
		"Compressed Report", "降噪率", "压缩率", "吸T完成", "原始输出", "吸后摘要",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("antigravity tool output must NOT contain %q, got:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "执行成功") || !strings.Contains(s, "压缩处理") {
		t.Errorf("expected natural-language result (执行成功 … 压缩处理), got:\n%s", s)
	}
}

// TestClaudeAutoOutputNaturalLanguage verifies XiT's `xit auto` stdout under
// effectiveAdapter()=="claude" is a short natural-language result, hiding all
// machine bookkeeping fields and any raw_log / .xit/runs path (the real bug:
// Claude's tool result area was still showing command:/exit_code:/raw_log: etc).
func TestClaudeAutoOutputNaturalLanguage(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=claude", "XIT_CLAUDE_SETTLE_MS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (claude) failed: %v\n%s", err, out)
	}
	s := string(out)
	for _, bad := range []string{
		"command:", "exit_code:", "status:", "reduction:", "saved_tokens:",
		"raw_log:", "key facts", "key_facts", ".xit/runs",
		"降噪率", "压缩率", "吸T完成", "原始输出", "吸后摘要",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("claude tool output must NOT contain %q, got:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "执行成功") || !strings.Contains(s, "压缩处理") {
		t.Errorf("expected natural-language result (执行成功 … 压缩处理), got:\n%s", s)
	}
}

// TestClaudeAutoDetectedFromAncestryNaturalLanguage verifies that WITHOUT
// CLAUDECODE/CLAUDE_CODE_SESSION_ID, but with a Claude Code process ancestor
// (mocked), `xit auto` still renders the natural-language result and hides all
// machine fields. This is the real-Claude case the user reported: the Bash
// tool's spawned subprocess may not inherit CLAUDECODE, so detection must also
// work from the process ancestor chain (the direct parent is the `claude`
// binary itself).
func TestClaudeAutoDetectedFromAncestryNaturalLanguage(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER, NO CLAUDECODE
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_TEST_ANCESTORS=/Users/x/.vscode/extensions/anthropic.claude-code/resources/native-binary/claude,bash",
		"XIT_CLAUDE_SETTLE_MS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "执行成功") || !strings.Contains(s, "压缩处理") {
		t.Errorf("expected natural-language result (claude detected via ancestry), got:\n%s", s)
	}
	for _, bad := range []string{
		"command:", "exit_code:", "status:", "reduction:", "saved_tokens:",
		"raw_log:", "key facts", "key_facts", ".xit/runs", "吸T完成", "压缩率", "原始输出",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("ancestry-detected claude output must NOT contain %q, got:\n%s", bad, s)
		}
	}
	data, err := os.ReadFile(filepath.Join(tmpHome, "state", "current-run.json"))
	if err != nil || !strings.Contains(string(data), `"adapter":"claude"`) {
		t.Fatalf("expected adapter=claude in state (ancestry-detected), got: %s (err %v)", data, err)
	}
}

// antigravityLineFor runs `xit antigravity statusline --json` against the given
// XIT_HOME and returns the rendered line.
func antigravityLineFor(t *testing.T, bin, home string) string {
	t.Helper()
	cmd := exec.Command(bin, "antigravity", "statusline", "--json")
	cmd.Env = append(os.Environ(), "HOME="+home, "XIT_HOME="+home, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	line, _ := data["line"].(string)
	return line
}

// TestAntigravityAutoWritesSettlingBeforeCompleted verifies the Antigravity auto
// flow first lingers in "正在收功中" (settling) and only then shows the final
// "本轮省 X.XXk Token" — so a single host sample can catch the settle state.
func TestAntigravityAutoWritesSettlingBeforeCompleted(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	auto := exec.Command(bin, "auto", "noisycmd")
	auto.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=antigravity", "XIT_ANTIGRAVITY_SETTLE_MS=1500")
	if err := auto.Start(); err != nil {
		t.Fatalf("start auto: %v", err)
	}

	// Sample the statusline during the settle buffer.
	sawSettling := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(antigravityLineFor(t, bin, tmpHome), "正在收功中") {
			sawSettling = true
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if err := auto.Wait(); err != nil {
		t.Fatalf("auto wait: %v", err)
	}
	if !sawSettling {
		t.Errorf("expected to observe '正在收功中' during the settle buffer")
	}

	// After completion the final line is the saved-token line, not settling.
	final := antigravityLineFor(t, bin, tmpHome)
	if !strings.Contains(final, "本轮省") || !strings.Contains(final, "Token") {
		t.Errorf("expected final '本轮省 X.XXk Token', got: %s", final)
	}
	if strings.Contains(final, "正在收功中") {
		t.Errorf("final line must not be settling, got: %s", final)
	}
	if strings.Contains(final, "约") {
		t.Errorf("final line must not contain 约, got: %s", final)
	}
}

// TestAntigravityAutoSettleDelayIsAdapterScoped verifies the settle sleep applies
// ONLY to XIT_ADAPTER=antigravity: a default-adapter run ignores the (large)
// settle env and never writes a settling state.
func TestAntigravityAutoSettleDelayIsAdapterScoped(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	start := time.Now()
	cmd := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_ANTIGRAVITY_SETTLE_MS=4000")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("auto failed: %v\n%s", err, out)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("default-adapter auto must not apply the 4s antigravity settle; took %v", elapsed)
	}
	data, err := os.ReadFile(filepath.Join(tmpHome, "state", "current-run.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(data), `"status":"settling"`) {
		t.Errorf("default-adapter auto must not write a settling state, got: %s", data)
	}
}

// TestAntigravityAutoDetectedFromAncestryNaturalLanguage verifies that WITHOUT
// XIT_ADAPTER, but with an Antigravity process ancestor (mocked), `xit auto`
// renders the natural-language result and hides all machine fields. This is the
// real-Agy case (Agy does not set XIT_ADAPTER on the tool process).
func TestAntigravityAutoDetectedFromAncestryNaturalLanguage(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_TEST_ANCESTORS=agy,bash,zsh", "XIT_ANTIGRAVITY_SETTLE_MS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "执行成功") || !strings.Contains(s, "压缩处理") {
		t.Errorf("expected natural-language result (detected via ancestry), got:\n%s", s)
	}
	for _, bad := range []string{
		"command:", "exit_code:", "status:", "reduction:", "saved_tokens:",
		"raw_log:", "key facts", "key_facts", ".xit/runs", "吸T完成", "压缩率", "原始输出",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("ancestry-detected antigravity output must NOT contain %q, got:\n%s", bad, s)
		}
	}
}

// TestAntigravityAutoSettlingDetectedFromAncestry verifies the settling buffer
// also runs when Antigravity is detected via ancestry (no XIT_ADAPTER).
func TestAntigravityAutoSettlingDetectedFromAncestry(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	auto := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER
	auto.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_TEST_ANCESTORS=antigravity-cli,bash", "XIT_ANTIGRAVITY_SETTLE_MS=1500")
	if err := auto.Start(); err != nil {
		t.Fatalf("start auto: %v", err)
	}
	sawSettling := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(antigravityLineFor(t, bin, tmpHome), "正在收功中") {
			sawSettling = true
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if err := auto.Wait(); err != nil {
		t.Fatalf("auto wait: %v", err)
	}
	if !sawSettling {
		t.Errorf("expected 正在收功中 during settle buffer (detected via ancestry)")
	}
	final := antigravityLineFor(t, bin, tmpHome)
	if !strings.Contains(final, "本轮省") || !strings.Contains(final, "Token") {
		t.Errorf("expected final 本轮省 X.XXk Token, got: %s", final)
	}
}

// TestAntigravitySavedTokenTruthful verifies the run-state saved_tokens equals the
// real savedBytes/4 (no fixed value, no clamp, no unit mixing).
func TestAntigravitySavedTokenTruthful(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 640 ]; do echo \"block line $i hello xit compress aaaa bbbb cccc dddd eeee ffff\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, "state", "current-run.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var st struct {
		RawBytes     int `json:"raw_bytes"`
		SummaryBytes int `json:"summary_bytes"`
		SavedBytes   int `json:"saved_bytes"`
		SavedTokens  int `json:"saved_tokens"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	wantSaved := st.RawBytes - st.SummaryBytes
	if wantSaved < 0 {
		wantSaved = 0
	}
	if st.SavedBytes != wantSaved {
		t.Errorf("saved_bytes %d != rawBytes-summaryBytes %d", st.SavedBytes, wantSaved)
	}
	if st.SavedTokens != st.SavedBytes/4 {
		t.Errorf("saved_tokens %d != saved_bytes/4 %d", st.SavedTokens, st.SavedBytes/4)
	}
	_ = out
}

// TestAntigravityStatuslineStaleRunningFallback verifies a stale running state
// (heartbeat older than the 15s freshness window) does NOT show 正在吸T中.
func TestAntigravityStatuslineStaleRunningFallback(t *testing.T) {
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	old := time.Now().Add(-2 * time.Minute).Format(time.RFC3339)
	state := `{"status":"running","adapter":"antigravity","started_at":"` + old + `","heartbeat_at":"` + old + `","command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	data := antigravityStatuslineJSON(t, tmpProject)
	line, _ := data["line"].(string)
	if strings.Contains(line, "正在吸T中") {
		t.Errorf("stale running must NOT show 正在吸T中, got: %s", line)
	}
}

// TestClaudeStatuslineRunningLabel verifies the Claude running line carries the
// adapter label: "Claude · 正在吸T中".
func TestClaudeStatuslineRunningLabel(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	state := `{"status":"running","adapter":"claude","started_at":"` + time.Now().Format(time.RFC3339) + `","heartbeat_at":"` + time.Now().Format(time.RFC3339) + `","command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

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
	if !strings.Contains(line, "Claude · 正在吸T中") {
		t.Errorf("expected 'Claude · 正在吸T中' for running autostate, got: %s", line)
	}
}

// claudeStatuslineLine returns the Claude statusline line for a fresh
// home/project pair (idle -> the merged 守护你的T/准备就绪 line).
func claudeStatuslineLine(t *testing.T, bin, home, project string) string {
	t.Helper()
	return claudeLine(t, bin, home, project)
}

// TestClaudeStatuslineIgnoresAntigravityState verifies an Antigravity-tagged run
// state does NOT leak into Claude's statusline (the real pollution bug).
func TestClaudeStatuslineIgnoresAntigravityState(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	ts := time.Now().Format(time.RFC3339)
	state := `{"status":"completed","adapter":"antigravity","finished_at":"` + ts + `","completed_at":"` + ts + `","saved_bytes":293640,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	line := claudeStatuslineLine(t, bin, tmpHome, tmpProject)
	for _, bad := range []string{"73.4", "Antigravity", "本轮省", "本次省", "10.28"} {
		if strings.Contains(line, bad) {
			t.Errorf("Claude must not show Antigravity data %q, got: %s", bad, line)
		}
	}
	if line != "吸T神功 · Claude · 守护你的T · 准备就绪" {
		t.Errorf("expected Claude merged idle line, got: %s", line)
	}
}

// TestClaudeStatuslineIgnoresLegacyState verifies a legacy run state with NO
// adapter field is ignored (Claude shows idle/ready, not stale saved tokens).
func TestClaudeStatuslineIgnoresLegacyState(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpProject, "state"), 0755)
	ts := time.Now().Format(time.RFC3339)
	state := `{"status":"completed","finished_at":"` + ts + `","completed_at":"` + ts + `","saved_bytes":293640,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpProject, "state", "current-run.json"), []byte(state), 0644)

	line := claudeStatuslineLine(t, bin, tmpHome, tmpProject)
	if strings.Contains(line, "本轮省") || strings.Contains(line, "本次省") || strings.Contains(line, "73.4") {
		t.Errorf("Claude must ignore legacy (no-adapter) saved tokens, got: %s", line)
	}
	if line != "吸T神功 · Claude · 守护你的T · 准备就绪" {
		t.Errorf("expected Claude merged idle line, got: %s", line)
	}
}

// TestClaudeStatuslineNoHitRate verifies the Claude statusline never shows a hit
// rate (the unreliable 命中率0% the user reported).
func TestClaudeStatuslineNoHitRate(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	tmpProject := t.TempDir()
	line := claudeStatuslineLine(t, bin, tmpHome, tmpProject)
	for _, bad := range []string{"命中率", "命中加成", "hit"} {
		if strings.Contains(line, bad) {
			t.Errorf("Claude statusline must not contain %q, got: %s", bad, line)
		}
	}
}

// claudeLine runs the Claude statusline, stripping any ambient
// CLAUDE_CODE_SESSION_ID (so session/idle-call state is fully controlled by the
// test). extra appends env (e.g. "XIT_TEST_SESSION_KEY=s1").
func claudeLine(t *testing.T, bin, home, project string, extra ...string) string {
	t.Helper()
	env := stripEnv(os.Environ(), "CLAUDE_CODE_SESSION_ID")
	env = append(env, "HOME="+home, "XIT_HOME="+project, "XIT_NONINTERACTIVE=1")
	env = append(env, extra...)
	cmd := exec.Command(bin, "claude", "statusline", "--json")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("claude statusline failed: %v\n%s", err, out)
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	line, _ := data["line"].(string)
	return line
}

// TestClaudeStatuslineIdleMergedLine verifies the idle line is a single
// poll-safe merge of 守护你的T and 准备就绪 — NOT a first/later session split and
// NOT time-based rotation. Real Claude Code does not reliably re-poll the
// statusline while sitting idle (confirmed by the user: it can stay on one
// rendering indefinitely), so any approach relying on a second poll is
// invisible in practice; the single visible call must always be correct.
func TestClaudeStatuslineIdleMergedLine(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	project := t.TempDir()

	want := "吸T神功 · Claude · 守护你的T · 准备就绪"
	// Repeated calls (same or different "session") all return the identical
	// merged line — no dependency on call count or session identity.
	for i, key := range []string{"s1", "s1", "s1", "s2", ""} {
		var l string
		if key == "" {
			l = claudeLine(t, bin, home, project)
		} else {
			l = claudeLine(t, bin, home, project, "XIT_TEST_SESSION_KEY="+key)
		}
		if l != want {
			t.Errorf("call %d expected merged idle line %q, got: %s", i, want, l)
		}
	}
}

// TestClaudeStatuslineSettling verifies an EXPLICIT settling state (written by
// `xit auto`, not age-based guessing) shows "正在收功中" and no saved tokens.
func TestClaudeStatuslineSettling(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	project := t.TempDir()
	_ = os.MkdirAll(filepath.Join(project, "state"), 0755)
	ts := time.Now().Format(time.RFC3339)
	state := `{"status":"settling","adapter":"claude","started_at":"` + ts + `","heartbeat_at":"` + ts + `","saved_bytes":41167,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(project, "state", "current-run.json"), []byte(state), 0644)

	l := claudeLine(t, bin, home, project)
	if !strings.Contains(l, "吸T完成 · Claude · 正在收功中") {
		t.Errorf("expected '吸T完成 · Claude · 正在收功中' for settling state, got: %s", l)
	}
	if strings.Contains(l, "本轮省") || strings.Contains(l, "本次省") {
		t.Errorf("settling must not show saved tokens, got: %s", l)
	}
}

// TestClaudeStatuslineStaleSettlingFallback verifies a stale settling state
// (heartbeat older than the freshness window) does NOT stick on 正在收功中.
func TestClaudeStatuslineStaleSettlingFallback(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	project := t.TempDir()
	_ = os.MkdirAll(filepath.Join(project, "state"), 0755)
	old := time.Now().Add(-30 * time.Second).Format(time.RFC3339)
	state := `{"status":"settling","adapter":"claude","started_at":"` + old + `","heartbeat_at":"` + old + `","saved_bytes":41167,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(project, "state", "current-run.json"), []byte(state), 0644)

	l := claudeLine(t, bin, home, project)
	if strings.Contains(l, "正在收功中") {
		t.Errorf("stale settling must NOT keep showing 正在收功中, got: %s", l)
	}
}

// TestClaudeStatuslineCompletedSingleLine verifies the completed state is a
// single poll-safe line merging 本轮省 (no 约) and the real 本轮共吸 N次 (no
// rotation — repeated calls return the identical line).
func TestClaudeStatuslineCompletedSingleLine(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	project := t.TempDir()
	_ = os.MkdirAll(filepath.Join(project, "state"), 0755)
	ts := time.Now().Format(time.RFC3339)
	state := `{"status":"completed","adapter":"claude","finished_at":"` + ts + `","completed_at":"` + ts + `","saved_bytes":41167,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(project, "state", "current-run.json"), []byte(state), 0644)
	_ = os.WriteFile(filepath.Join(project, "state", "claude-run-count.json"),
		[]byte(`{"session_key":"s1","count":2,"updated_at":"`+ts+`"}`), 0644)

	want := "吸T完成 · Claude · 本轮省 10.29k Token · 本轮共吸 2次"
	for i := 0; i < 2; i++ {
		l := claudeLine(t, bin, home, project, "XIT_TEST_SESSION_KEY=s1")
		if !strings.Contains(l, want) {
			t.Errorf("call %d expected single line %q, got: %s", i, want, l)
		}
		if strings.Contains(l, "本次省") || strings.Contains(l, "命中率") || strings.Contains(l, "约") {
			t.Errorf("call %d must not contain 本次省/命中率/约, got: %s", i, l)
		}
	}
}

// TestClaudeNoFakeCount verifies that without a real session the completed line
// shows 本轮省 only — never a fake "本轮共吸".
func TestClaudeNoFakeCount(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	project := t.TempDir()
	_ = os.MkdirAll(filepath.Join(project, "state"), 0755)
	ts := time.Now().Format(time.RFC3339)
	state := `{"status":"completed","adapter":"claude","finished_at":"` + ts + `","completed_at":"` + ts + `","saved_bytes":41167,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(project, "state", "current-run.json"), []byte(state), 0644)
	// A counter exists, but there is NO session (CLAUDE_CODE_SESSION_ID stripped,
	// no XIT_TEST_SESSION_KEY) -> must NOT show 本轮共吸.
	_ = os.WriteFile(filepath.Join(project, "state", "claude-run-count.json"),
		[]byte(`{"session_key":"s1","count":5,"updated_at":"`+ts+`"}`), 0644)

	l := claudeLine(t, bin, home, project)
	if !strings.Contains(l, "吸T完成 · Claude · 本轮省 10.29k Token") {
		t.Errorf("expected 本轮省 line, got: %s", l)
	}
	if strings.Contains(l, "本轮共吸") {
		t.Errorf("must NOT show 本轮共吸 without a real session, got: %s", l)
	}
}

// TestClaudeCompletedOldGoesIdle verifies a completed run older than the fresh
// window (>30s) returns to the merged idle line — NO 等待下轮发功 (waiting state
// removed; Claude restarts the cycle on next input, like Agy).
func TestClaudeCompletedOldGoesIdle(t *testing.T) {
	bin := buildXit(t)
	home := t.TempDir()
	project := t.TempDir()
	_ = os.MkdirAll(filepath.Join(project, "state"), 0755)
	past := time.Now().Add(-90 * time.Second).Format(time.RFC3339) // > 30s fresh window
	state := `{"status":"completed","adapter":"claude","finished_at":"` + past + `","completed_at":"` + past + `","saved_bytes":41167,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(project, "state", "current-run.json"), []byte(state), 0644)

	if l := claudeLine(t, bin, home, project); l != "吸T神功 · Claude · 守护你的T · 准备就绪" {
		t.Errorf("old completed expected merged idle line, got: %s", l)
	}
	if l := claudeLine(t, bin, home, project); strings.Contains(l, "等待下轮发功") {
		t.Errorf("must NOT show 等待下轮发功 (waiting removed), got: %s", l)
	}
}

// TestClaudeRunCountFromAuto verifies `xit auto` detected as Claude (via
// XIT_TEST_CLAUDECODE) tags adapter=claude and increments the real run counter,
// which the statusline merges as 本轮共吸 N次. Settle delay forced to 0 for speed.
func TestClaudeRunCountFromAuto(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	home := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	runAuto := func() {
		cmd := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER
		cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+home, "XIT_NONINTERACTIVE=1",
			"XIT_TEST_CLAUDECODE=1", "XIT_TEST_SESSION_KEY=sess-A", "XIT_CLAUDE_SETTLE_MS=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("auto failed: %v\n%s", err, out)
		}
	}
	runAuto()
	runAuto()

	// State tagged adapter=claude.
	data, err := os.ReadFile(filepath.Join(home, "state", "current-run.json"))
	if err != nil || !strings.Contains(string(data), `"adapter":"claude"`) {
		t.Fatalf("expected adapter=claude in state, got: %s (err %v)", data, err)
	}
	// Counter = 2; statusline merges 本轮共吸 2次 (real session via XIT_TEST_SESSION_KEY).
	l := claudeLine(t, bin, home, home, "XIT_TEST_SESSION_KEY=sess-A")
	if !strings.Contains(l, "本轮共吸 2次") {
		t.Errorf("expected 本轮共吸 2次 after two claude runs, got: %s", l)
	}
}

// TestClaudeAutoWritesSettlingBeforeCompleted verifies the Claude auto flow first
// lingers in "正在收功中" (explicit settling, mirrors Agy) and only then shows the
// final "本轮省 X.XXk Token" — so a single host sample can catch the settle state.
func TestClaudeAutoWritesSettlingBeforeCompleted(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	auto := exec.Command(bin, "auto", "noisycmd")
	auto.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=claude", "XIT_TEST_SESSION_KEY=sess-B", "XIT_CLAUDE_SETTLE_MS=1500")
	if err := auto.Start(); err != nil {
		t.Fatalf("start auto: %v", err)
	}

	sawSettling := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(claudeLine(t, bin, tmpHome, tmpHome, "XIT_TEST_SESSION_KEY=sess-B"), "正在收功中") {
			sawSettling = true
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if err := auto.Wait(); err != nil {
		t.Fatalf("auto wait: %v", err)
	}
	if !sawSettling {
		t.Errorf("expected to observe '正在收功中' during the Claude settle buffer")
	}

	final := claudeLine(t, bin, tmpHome, tmpHome, "XIT_TEST_SESSION_KEY=sess-B")
	if !strings.Contains(final, "本轮省") || !strings.Contains(final, "Token") {
		t.Errorf("expected final '本轮省 X.XXk Token', got: %s", final)
	}
	if strings.Contains(final, "正在收功中") {
		t.Errorf("final line must not be settling, got: %s", final)
	}
	if strings.Contains(final, "约") {
		t.Errorf("final line must not contain 约, got: %s", final)
	}
}

// TestClaudeAutoSettleDelayIsAdapterScoped verifies the Claude settle sleep
// applies ONLY to effectiveAdapter()=="claude": a default-adapter run ignores
// the (large) settle env and never writes a settling state.
func TestClaudeAutoSettleDelayIsAdapterScoped(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	start := time.Now()
	cmd := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER, no CLAUDECODE
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_CLAUDE_SETTLE_MS=4000")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("auto failed: %v\n%s", err, out)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("default-adapter auto must not apply the 4s claude settle; took %v", elapsed)
	}
	data, err := os.ReadFile(filepath.Join(tmpHome, "state", "current-run.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(data), `"status":"settling"`) {
		t.Errorf("default-adapter auto must not write a settling state, got: %s", data)
	}
}

// codexAssertNoFooterOrMachineFields asserts the Codex per-tool-call output
// (xit auto stdout) NEVER contains the XiT footer (it must only appear once,
// at the end of the turn's final answer, via the Stop hook — see
// internal/codexhook), no machine bookkeeping fields, and no meaningless
// numeric bullets (the literal "- 1" / "- 2185" bug from the prior round).
func codexAssertNoFooterOrMachineFields(t *testing.T, s string) {
	t.Helper()
	for _, bad := range []string{
		"command:", "exit_code:", "status:", "reduction:", "saved_tokens:",
		"raw_log:", "key facts", "key_facts", ".xit/runs",
		"原始输出", "吸后摘要", "压缩率", "吸T完成",
		"吸T神功 · Codex", "本次省", "本轮共吸",
		"PostToolUse hook context", "hook context:", "本轮执行过 XiT",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("codex per-tool-call output must NOT contain %q (footer belongs only in the turn's final answer), got:\n%s", bad, s)
		}
	}
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		bullet := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "-"), "•"))
		if bullet != trimmed && bullet != "" {
			if _, err := strconv.Atoi(bullet); err == nil {
				t.Errorf("codex output must not contain a bare numeric bullet %q, got:\n%s", trimmed, s)
			}
		}
	}
}

// TestCodexAutoNeverShowsFooterRegardlessOfTokenSize verifies that NO XiT
// footer/branding ever appears in `xit auto` stdout under Codex, for both a
// large (>=1000 tokens) and small (<1000 tokens) compressed result — the
// footer now belongs exclusively to the Stop-hook-driven final answer.
func TestCodexAutoNeverShowsFooterRegardlessOfTokenSize(t *testing.T) {
	bin := buildXit(t)
	for _, n := range []int{50, 3000} {
		tmpPath := t.TempDir()
		tmpHome := t.TempDir()
		tool := filepath.Join(tmpPath, "noisycmd")
		os.WriteFile(tool, []byte(fmt.Sprintf("#!/bin/sh\ni=0\nwhile [ $i -lt %d ]; do echo \"line $i hello xit compress aaaa bbbb cccc dddd\"; i=$((i+1)); done", n)), 0755)

		cmd := exec.Command(bin, "auto", "noisycmd")
		cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=codex")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("auto noisycmd (codex, n=%d) failed: %v\n%s", n, err, out)
		}
		codexAssertNoFooterOrMachineFields(t, string(out))
	}
}

// TestCodexAutoNoLowQualityBullets reproduces the exact reported bug: generic
// fallback (low-confidence) output must NOT render bare numeric KeyFacts
// ("- 1" / "- 2185") — and with no real diagnostic content, the tool output
// must be the minimal acknowledgement, nothing else (no footer either).
func TestCodexAutoNoLowQualityBullets(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	// Generic/unrecognized command -> safeFallback (Confidence="low") whose
	// KeyFacts are bare stdout_lines/stderr_lines counts.
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 2185 ]; do echo \"line $i\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=codex")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (codex) failed: %v\n%s", err, out)
	}
	s := string(out)
	codexAssertNoFooterOrMachineFields(t, s)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "命令执行成功，无需展开重复输出。" {
		t.Errorf("low-confidence fallback with no diagnostics must yield only the minimal acknowledgement plus the per-command status line, got:\n%s", s)
	}
	if len(lines) == 2 && !strings.HasPrefix(lines[1], "XiT · auto · ") {
		t.Errorf("expected the per-command status line as the second line, got:\n%s", s)
	}
}

func TestCodexAutoWithInjectedTurnIdentityNoPerToolFooter(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb cccc dddd\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=codex", "XIT_CODEX_SESSION_ID=session-test-1", "XIT_CODEX_TURN_ID=turn-test-1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (codex) failed: %v\n%s", err, out)
	}
	s := string(out)
	codexAssertNoFooterOrMachineFields(t, s)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) != 2 || lines[0] != "命令执行成功，无需展开重复输出。" {
		t.Fatalf("expected minimal Codex per-tool output plus the per-command status line, got:\n%s", s)
	}
	if len(lines) == 2 && !strings.HasPrefix(lines[1], "XiT · auto · ") {
		t.Fatalf("expected the per-command status line as the second line, got:\n%s", s)
	}
}

// TestCodexAutoPreservesRealDiagnostics verifies that a real, high-confidence
// filter result (a failing go test, with real file:line content) is preserved
// verbatim, with NO footer/branding appended anywhere in the tool output.
func TestCodexAutoPreservesRealDiagnostics(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	modDir := t.TempDir()
	os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module codextestmod\ngo 1.21\n"), 0644)
	os.WriteFile(filepath.Join(modDir, "main_test.go"), []byte(`package main
import "testing"
func TestFails(t *testing.T) {
	t.Errorf("expected 3, got 2 -- long enough message to push raw output past the 400 byte forced-compress threshold for a failing go test run so the codex summary path actually engages for this verification case right now today -- adding considerably more padding text here to be safe across environments and caches so this reliably exceeds the four hundred byte minimum threshold required to force compression on a failing test run every single time without fail")
}

func TestCodexAutoLowConfidenceFailurePreservesFileLineDiagnostic(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "auto", "bash", "-lc", "printf '%s\\n' 'setup noise before failure'; printf '%s\\n' 'internal/foo_test.go:42: expected 3, got 2'; i=0; while [ $i -lt 40 ]; do echo \"noise $i padding padding padding padding\"; i=$((i+1)); done; exit 1")
	cmd.Env = append(noXitAdapterEnv(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=codex")
	out, _ := cmd.CombinedOutput() // exit 1 is expected
	s := string(out)
	codexAssertNoFooterOrMachineFields(t, s)
	if !strings.Contains(s, "internal/foo_test.go:42: expected 3, got 2") {
		t.Fatalf("expected real file:line diagnostic preserved, got:\n%s", s)
	}
	if strings.TrimSpace(s) == "命令以退出码 1 结束，输出已压缩。" {
		t.Fatalf("generic failure text must not replace concrete diagnostics")
	}
}
`), 0644)

	cmd := exec.Command(bin, "auto", "go", "test", "-v", "./...")
	cmd.Dir = modDir
	cmd.Env = append(noXitAdapterEnv(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=codex")
	out, _ := cmd.CombinedOutput() // go test exits non-zero; that's expected
	s := string(out)
	codexAssertNoFooterOrMachineFields(t, s)
	if !strings.Contains(s, "main_test.go:") {
		t.Errorf("expected real diagnostic file:line preserved, got:\n%s", s)
	}
}

// TestCodexAutoDetectedFromAncestry verifies Codex is detected from the process
// ancestor chain (no XIT_ADAPTER, no env signal exists for Codex — audited:
// unlike Claude's CLAUDECODE, there is no equivalent Codex env var), matching
// the real ancestor path the user reported: "~/.local/node/current/bin/codex".
// The tool output still has no footer; only the state's adapter field proves
// detection worked.
func TestCodexAutoDetectedFromAncestry(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	cmd := exec.Command(bin, "auto", "noisycmd") // NO XIT_ADAPTER
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_TEST_ANCESTORS=/Users/x/.local/node/current/bin/codex,bash")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto failed: %v\n%s", err, out)
	}
	codexAssertNoFooterOrMachineFields(t, string(out))
	data, err := os.ReadFile(filepath.Join(tmpHome, "state", "current-run.json"))
	if err != nil || !strings.Contains(string(data), `"adapter":"codex"`) {
		t.Fatalf("expected adapter=codex in state (ancestry-detected), got: %s (err %v)", data, err)
	}
}

// TestCodexAutoIsolatedFromOtherAdapterState verifies a pre-existing
// Antigravity/Claude run-state file in the same XIT_HOME does not leak into
// Codex's tool output.
func TestCodexAutoIsolatedFromOtherAdapterState(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	_ = os.MkdirAll(filepath.Join(tmpHome, "state"), 0755)
	ts := time.Now().Format(time.RFC3339)
	stale := `{"status":"completed","adapter":"antigravity","finished_at":"` + ts + `","completed_at":"` + ts + `","saved_bytes":293640,"command":"go test"}`
	_ = os.WriteFile(filepath.Join(tmpHome, "state", "current-run.json"), []byte(stale), 0644)

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1", "XIT_ADAPTER=codex")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto noisycmd (codex) failed: %v\n%s", err, out)
	}
	s := string(out)
	for _, bad := range []string{"Antigravity", "本轮省", "73.4", "293640"} {
		if strings.Contains(s, bad) {
			t.Errorf("codex output must not show Antigravity data %q, got:\n%s", bad, s)
		}
	}
}

// TestCodexAutoAccumulatesIntoTurnState verifies an end-to-end `xit auto`
// invocation under Codex (effectiveAdapter()=="codex" + real
// XIT_CODEX_SESSION_ID/XIT_CODEX_TURN_ID env, exactly as PreToolUse injects
// them) accumulates run_count/saved_tokens_total in codex-turns/<session>/<turn>.json — and
// that two calls within the SAME turn ADD UP rather than overwrite.
func TestCodexTurnAccumulatesTwoRuns(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	runOnce := func() {
		cmd := exec.Command(bin, "auto", "noisycmd")
		cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
			"XIT_ADAPTER=codex", "XIT_CODEX_SESSION_ID=s1", "XIT_CODEX_TURN_ID=t1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("auto failed: %v\n%s", err, out)
		}
		codexAssertNoFooterOrMachineFields(t, string(out))
	}
	runOnce()
	runOnce()

	matches, err := filepath.Glob(filepath.Join(tmpHome, "state", "codex-turns", "*", "*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one codex turn state file, matches=%v err=%v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("expected codex turn state file to be readable: %v", err)
	}
	var st struct {
		SessionID        string `json:"session_id"`
		TurnID           string `json:"turn_id"`
		RunCount         int    `json:"run_count"`
		SavedTokensTotal int    `json:"saved_tokens_total"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("invalid turn state JSON: %v\n%s", err, data)
	}
	if st.SessionID != "s1" || st.TurnID != "t1" {
		t.Errorf("expected session=s1 turn=t1, got: %+v", st)
	}
	if st.RunCount != 2 {
		t.Errorf("expected run_count=2 after two xit auto calls in the same turn, got %d", st.RunCount)
	}
	if st.SavedTokensTotal <= 0 {
		t.Errorf("expected saved_tokens_total > 0, got %d", st.SavedTokensTotal)
	}
}

// TestVSCodeBridgeRunCountMatchesCodexFooterTurnState verifies the VS Code
// Dashboard's "本轮共吸" card uses the exact same per-turn counter as the
// Codex CLI footer's "本轮共吸 N次" — not a different count (e.g. today's
// total run count). The PreToolUse hook side is simulated by calling
// StartIfCodexVSCode directly (same call this test process would make if it
// were the hook subprocess) and injecting the resulting run id via
// XIT_VSCODE_BRIDGE_RUN_ID, exactly as internal/codexhook/rewrite.go does.
func TestVSCodeBridgeRunCountMatchesCodexFooterTurnState(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	t.Setenv("VSCODE_PID", "4242")
	runID := vscodebridge.NewRunID()
	if _, ok := vscodebridge.StartIfCodexVSCode(tmpHome, tmpPath, "noisycmd", "s1", runID, time.Now()); !ok {
		t.Fatal("expected vscode bridge run.started to be recorded")
	}

	cmd := exec.Command(bin, "auto", "noisycmd")
	cmd.Dir = tmpPath
	cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=codex", "XIT_CODEX_SESSION_ID=s1", "XIT_CODEX_TURN_ID=t1",
		"XIT_VSCODE_BRIDGE_RUN_ID="+runID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto failed: %v\n%s", err, out)
	}
	codexAssertNoFooterOrMachineFields(t, string(out))

	data, err := os.ReadFile(filepath.Join(tmpHome, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatalf("expected bridge events file: %v", err)
	}
	var bridgeRunCount *int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct {
			Event    string `json:"event"`
			RunCount *int   `json:"run_count"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid bridge event JSON: %v\n%s", err, line)
		}
		if e.Event == "run.finished" {
			bridgeRunCount = e.RunCount
		}
	}
	if bridgeRunCount == nil {
		t.Fatalf("expected a run.finished event carrying run_count, events:\n%s", data)
	}

	matches, err := filepath.Glob(filepath.Join(tmpHome, "state", "codex-turns", "*", "*.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected one codex turn state file (what the Stop hook footer reads), matches=%v err=%v", matches, err)
	}
	turnData, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var turnState struct {
		RunCount int `json:"run_count"`
	}
	if err := json.Unmarshal(turnData, &turnState); err != nil {
		t.Fatalf("invalid turn state JSON: %v\n%s", err, turnData)
	}

	if *bridgeRunCount != turnState.RunCount {
		t.Fatalf("bridge event run_count=%d does not match the Codex footer's turn RunCount=%d", *bridgeRunCount, turnState.RunCount)
	}
	if turnState.RunCount != 1 {
		t.Fatalf("expected RunCount=1 after a single xit auto call, got %d", turnState.RunCount)
	}
}

// TestCodexFullChainPostToolUseAndStopSeeRealTurnState is a real-CLI-binary
// regression test for a confirmed bug: PostToolUse and Stop returned `{}`
// (turn "not found") even though `xit auto` had genuinely run twice and
// accumulated real savings for the exact same session_id/turn_id. Root cause:
// resolveCodexHome() preferred the hook payload's "cwd" field over an
// explicitly-set XIT_HOME env var, so the four lifecycle hooks (which DO
// receive a JSON payload with "cwd") resolved to a different .xit directory
// than `xit auto` itself (which has no payload at all and only ever consults
// XIT_HOME/process cwd via xitHome()). This test deliberately sets XIT_HOME
// AND includes a "cwd" field pointing at a DIFFERENT directory in every hook
// JSON payload — exactly the divergent-input combination that exposed the
// bug — and drives every step through the real compiled binary with real
// stdin JSON (not direct Go function calls), so a future regression in JSON
// tags, state-path computation, or env-prefixed command detection would also
// be caught here.
func TestCodexFullChainPostToolUseAndStopSeeRealTurnState(t *testing.T) {
	bin := buildXit(t)
	tmpHome := t.TempDir()
	otherCwd := t.TempDir() // deliberately NOT tmpHome — the divergent signal
	tmpPath := t.TempDir()
	tool := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(tool, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 300 ]; do echo \"line $i hello xit compress aaaa bbbb\"; i=$((i+1)); done"), 0755)

	runHook := func(sub, payload string) string {
		cmd := exec.Command(bin, "codex-hook", sub)
		cmd.Stdin = strings.NewReader(payload)
		cmd.Env = append(noXitAdapterEnv(), "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("codex-hook %s failed: %v\n%s", sub, err, out)
		}
		return string(out)
	}

	runHook("user-prompt-submit", `{"session_id":"session-test-1","turn_id":"turn-test-1","prompt":"test","hook_event_name":"UserPromptSubmit","cwd":"`+otherCwd+`"}`)

	runAuto := func() {
		cmd := exec.Command(bin, "auto", "noisycmd")
		cmd.Env = append(noXitAdapterEnv(), "PATH="+tmpPath, "XIT_HOME="+tmpHome, "XIT_NONINTERACTIVE=1",
			"XIT_ADAPTER=codex", "XIT_CODEX_SESSION_ID=session-test-1", "XIT_CODEX_TURN_ID=turn-test-1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("auto failed: %v\n%s", err, out)
		}
	}
	runAuto()
	runAuto()

	postOut := runHook("post-tool-use", `{"session_id":"session-test-1","turn_id":"turn-test-1","hook_event_name":"PostToolUse","cwd":"`+otherCwd+`","tool_name":"Bash","tool_use_id":"tool-test-2","tool_input":{"command":"XIT_ADAPTER=codex XIT_CODEX_SESSION_ID='session-test-1' XIT_CODEX_TURN_ID='turn-test-1' xit auto bash -lc 'echo test'"},"tool_response":{"success":true}}`)
	if postOut != "" {
		t.Fatalf("PostToolUse must be empty stdout to avoid visible hook context, got: %q", postOut)
	}

	stopPayload1 := `{"session_id":"session-test-1","turn_id":"turn-test-1","hook_event_name":"Stop","cwd":"` + otherCwd + `","stop_hook_active":false,"last_assistant_message":"第一轮测试完成。"}`
	stopOut1 := runHook("stop", stopPayload1)
	var stopResp1 map[string]interface{}
	if err := json.Unmarshal([]byte(stopOut1), &stopResp1); err != nil {
		t.Fatalf("Stop #1 stdout is not valid JSON: %v\n%s", err, stopOut1)
	}
	if stopResp1["decision"] != "block" {
		t.Fatalf("expected Stop #1 decision=block (footer missing, real run_count>0), got: %s", stopOut1)
	}
	reason, _ := stopResp1["reason"].(string)
	if !strings.Contains(reason, "本轮共吸 2次") {
		t.Errorf("expected Stop #1 reason to contain '本轮共吸 2次', got: %q", reason)
	}

	stopPayload2 := `{"session_id":"session-test-1","turn_id":"turn-test-1","hook_event_name":"Stop","cwd":"` + otherCwd + `","stop_hook_active":true,"last_assistant_message":"第一轮测试完成。"}`
	stopOut2 := runHook("stop", stopPayload2)
	if strings.TrimSpace(stopOut2) != "{}" {
		t.Fatalf("expected Stop #2 (stop_hook_active=true) to allow with {} (loop prevention), got: %q", stopOut2)
	}
}

// TestEffectiveAdapterCodex covers Codex-specific effectiveAdapter() cases:
// explicit env, ancestor basename match, and rejection of substring/false
// matches (node, codex-flow, a path merely containing "codex").
func TestEffectiveAdapterCodex(t *testing.T) {
	for _, k := range []string{"XIT_ADAPTER", "XIT_TEST_ANCESTORS", "XIT_TEST_CLAUDECODE"} {
		orig, had := os.LookupEnv(k)
		key := k
		t.Cleanup(func() {
			if had {
				os.Setenv(key, orig)
			} else {
				os.Unsetenv(key)
			}
		})
	}

	cases := []struct {
		name, adapter, ancestors, want string
	}{
		{"explicit codex", "codex", "bash", "codex"},
		{"ancestor codex basename", "", "/Users/x/.local/node/current/bin/codex,bash", "codex"},
		{"antigravity ancestry wins over codex ancestor", "", "agy,codex", "antigravity"},
		{"plain node must not match codex", "", "node,bash", ""},
		{"codex-flow must not substring-match", "", "codex-flow,bash", ""},
		{"dir containing codex must not match", "", "/Users/codex-fan/bin/myapp,bash", ""},
	}
	for _, c := range cases {
		if c.adapter == "" {
			os.Unsetenv("XIT_ADAPTER")
		} else {
			os.Setenv("XIT_ADAPTER", c.adapter)
		}
		os.Setenv("XIT_TEST_ANCESTORS", c.ancestors)
		os.Setenv("XIT_TEST_CLAUDECODE", "0")
		if got := effectiveAdapter(); got != c.want {
			t.Errorf("%s: effectiveAdapter()=%q want %q", c.name, got, c.want)
		}
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

// TestAutoOpencodeToolOutputTwoLineFooter verifies that OpenCode tool cards
// show only the fixed two-line XiT footer for pure repeated success output.
func TestAutoOpencodeToolOutputTwoLineFooter(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	// Fake git that produces high-noise output (>100 lines triggers compression).
	gitPath := filepath.Join(tmpPath, "git")
	os.WriteFile(gitPath, []byte("#!/bin/sh\nfor i in $(seq 1 200); do echo \"+ line $i changed\"; done"), 0755)

	cmd := exec.Command(bin, "auto", "git", "diff")
	cmd.Env = append(os.Environ(),
		"PATH="+tmpPath,
		"XIT_ORIGINAL_GIT="+gitPath,
		"XIT_HOME="+tmpHome,
		"XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=opencode",
		"XIT_OPENCODE_TURN_KEY=turn-git-"+strings.ReplaceAll(t.Name(), "/", "-"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("auto git diff (opencode) failed: %v\n%s", err, out)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "吸T神功 · OpenCode · 守护你的T") ||
		!strings.Contains(outStr, "本次省 ") ||
		!strings.Contains(outStr, "本轮共吸 1次") {
		t.Errorf("expected OpenCode two-line footer, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "命令执行成功，无需展开重复输出。") {
		t.Errorf("pure success must not include generic success line, got:\n%s", outStr)
	}
	for _, bad := range []string{"吸T完成", "raw_log:", "command:", "exit_code:", "saved_tokens:", ".xit/runs"} {
		if strings.Contains(outStr, bad) {
			t.Errorf("OpenCode tool output must not contain %q, got:\n%s", bad, outStr)
		}
	}
}

func TestAutoOpencodeTurnCountSameUserMessageAndReset(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	toolPath := filepath.Join(tmpPath, "noisycmd")
	os.WriteFile(toolPath, []byte("#!/bin/sh\ni=0\nwhile [ $i -lt 240 ]; do echo \"line $i hello xit compress aaaa bbbb cccc dddd\"; i=$((i+1)); done"), 0755)

	run := func(turnKey string) string {
		cmd := exec.Command(bin, "auto", "noisycmd")
		cmd.Env = append(noXitAdapterEnv(),
			"PATH="+tmpPath,
			"XIT_HOME="+tmpHome,
			"XIT_NONINTERACTIVE=1",
			"XIT_ADAPTER=opencode",
			"XIT_OPENCODE_TURN_KEY="+turnKey,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("auto noisycmd (opencode) failed: %v\n%s", err, out)
		}
		return string(out)
	}

	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("本轮共吸 %d次", i)
		if got := run("turn-1"); !strings.Contains(got, "吸T神功 · OpenCode · 守护你的T") || !strings.Contains(got, want) {
			t.Fatalf("same turn call %d: expected %q footer, got:\n%s", i, want, got)
		}
	}
	if st := opencodehook.ReadTurnStateByKey(tmpHome, "turn-1"); st == nil || st.RunCount != 3 {
		t.Fatalf("same turn state count=%+v want run_count=3", st)
	}
	if got := run("turn-2"); !strings.Contains(got, "本轮共吸 1次") || strings.Contains(got, "本轮共吸 4次") {
		t.Fatalf("new turn must reset footer count to 1, got:\n%s", got)
	}
	if st := opencodehook.ReadTurnStateByKey(tmpHome, "turn-2"); st == nil || st.RunCount != 1 {
		t.Fatalf("new turn state count=%+v want run_count=1", st)
	}
}

func TestAutoOpencodeNoTurnSignalOmitsCount(t *testing.T) {
	out := buildOpenCodeToolOutput(nil, 4000, 0, false, nil)
	for _, bad := range []string{"本轮共吸"} {
		if strings.Contains(out, bad) {
			t.Fatalf("OpenCode tool output must not contain %q, got:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "吸T神功 · OpenCode · 守护你的T") || !strings.Contains(out, "本次省 约 1.00k Token") {
		t.Fatalf("fallback output missing OpenCode no-count footer, got:\n%s", out)
	}
}

func TestOpenCodeFinalFooterTokenFormattingEdges(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{999, "本次省 999 Token"},
		{1000, "本次省 约 1.00k Token"},
		{9500, "本次省 约 9.50k Token"},
		{10200, "本次省 约 10.20k Token"},
		{10800, "本次省 约 10.80k Token"},
	}
	for _, c := range cases {
		got := "本次省 " + formatTokenHuman(c.tokens)
		if !strings.Contains(got, c.want) {
			t.Errorf("tokens=%d expected %q, got:\n%s", c.tokens, c.want, got)
		}
	}
}

func TestAutoOpencodeTokenTruthThreeSizes(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	sizes := []int{120, 360, 900}
	seenSavedTokens := map[int]bool{}

	for _, n := range sizes {
		toolName := fmt.Sprintf("noise%d", n)
		toolPath := filepath.Join(tmpPath, toolName)
		os.WriteFile(toolPath, []byte(fmt.Sprintf("#!/bin/sh\ni=0\nwhile [ $i -lt %d ]; do echo \"line $i hello xit compress aaaa bbbb cccc dddd eeee ffff\"; i=$((i+1)); done", n)), 0755)

		cmd := exec.Command(bin, "auto", toolName)
		cmd.Env = append(noXitAdapterEnv(),
			"PATH="+tmpPath,
			"XIT_HOME="+tmpHome,
			"XIT_NONINTERACTIVE=1",
			"XIT_ADAPTER=opencode",
			"XIT_OPENCODE_TURN_KEY=turn-"+toolName,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("auto %s (opencode) failed: %v\n%s", toolName, err, out)
		}
		var st struct {
			RawBytes     int `json:"raw_bytes"`
			SummaryBytes int `json:"summary_bytes"`
			SavedBytes   int `json:"saved_bytes"`
			SavedTokens  int `json:"saved_tokens"`
		}
		data, err := os.ReadFile(filepath.Join(tmpHome, "state", "current-run.json"))
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if err := json.Unmarshal(data, &st); err != nil {
			t.Fatalf("invalid state JSON: %v\n%s", err, data)
		}
		wantSaved := st.RawBytes - st.SummaryBytes
		if wantSaved < 0 {
			wantSaved = 0
		}
		if st.SavedBytes != wantSaved {
			t.Fatalf("%s saved_bytes=%d want raw-summary=%d (raw=%d summary=%d)", toolName, st.SavedBytes, wantSaved, st.RawBytes, st.SummaryBytes)
		}
		if st.SavedTokens != st.SavedBytes/4 {
			t.Fatalf("%s saved_tokens=%d want saved_bytes/4=%d", toolName, st.SavedTokens, st.SavedBytes/4)
		}
		if !strings.Contains(string(out), formatTokenHuman(st.SavedTokens)) || !strings.Contains(string(out), "本次省") || !strings.Contains(string(out), "本轮共吸 1次") {
			t.Fatalf("%s output must include OpenCode current-run footer stats, got:\n%s", toolName, out)
		}
		turnState := opencodehook.ReadTurnStateByKey(tmpHome, "turn-"+toolName)
		if turnState == nil || turnState.SavedTokensTotal != st.SavedTokens || turnState.RunCount != 1 {
			t.Fatalf("%s turn state = %+v want saved=%d run_count=1", toolName, turnState, st.SavedTokens)
		}
		seenSavedTokens[st.SavedTokens] = true
	}
	if len(seenSavedTokens) != len(sizes) {
		t.Fatalf("expected distinct saved token counts for sizes %v, got %v", sizes, seenSavedTokens)
	}
}

func TestAutoOpencodeFailureKeepsDiagnosticAndTwoLineFooter(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	tmpHome := t.TempDir()
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash not found: %v", err)
	}
	first := exec.Command(bin, "auto", "bash", "-lc", `for i in {1..1800}; do echo "noise-$i aaaa bbbb cccc"; done`)
	first.Env = append(noXitAdapterEnv(),
		"PATH="+tmpPath,
		"XIT_ORIGINAL_BASH="+bashPath,
		"XIT_HOME="+tmpHome,
		"XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=opencode",
		"XIT_OPENCODE_TURN_KEY=turn-failure",
	)
	if out, err := first.CombinedOutput(); err != nil {
		t.Fatalf("first opencode run failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "auto", "bash", "-lc", `for i in {1..1800}; do echo "noise-$i aaaa bbbb cccc"; done; echo "internal/opencode_test.go:42: expected 3, got 2" >&2; exit 1`)
	cmd.Env = append(noXitAdapterEnv(),
		"PATH="+tmpPath,
		"XIT_ORIGINAL_BASH="+bashPath,
		"XIT_HOME="+tmpHome,
		"XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=opencode",
		"XIT_OPENCODE_TURN_KEY=turn-failure",
	)
	out, _ := cmd.CombinedOutput() // exit 1 is expected
	s := string(out)
	for _, want := range []string{
		"internal/opencode_test.go:42: expected 3, got 2",
		"吸T神功 · OpenCode · 守护你的T",
		"本次省 约 ",
		"本轮共吸 2次",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, s)
		}
	}
	for _, bad := range []string{"raw_log:", "吸后摘要", "本次节省", "压缩率", "command:", "exit_code:", "status:", "saved_tokens:", ".xit/runs"} {
		if strings.Contains(s, bad) {
			t.Fatalf("OpenCode output must hide %q, got:\n%s", bad, s)
		}
	}
	if st := opencodehook.ReadTurnStateByKey(tmpHome, "turn-failure"); st == nil || st.RunCount != 2 || st.SavedTokensTotal <= 0 {
		t.Fatalf("failure should still accumulate OpenCode turn state, got %+v", st)
	}
}

// TestAutoOpencodeEnvNotLeakedToChild verifies that XIT_ADAPTER and
// OpenCode hook env vars are stripped from the child process environment.
func TestAutoOpencodeEnvNotLeakedToChild(t *testing.T) {
	bin := buildXit(t)
	tmpPath := t.TempDir()
	// Fake "env" binary that prints XIT_ADAPTER from its own environment.
	envScript := filepath.Join(tmpPath, "env")
	os.WriteFile(envScript, []byte("#!/bin/sh\nprintenv XIT_ADAPTER; printenv XIT_OPENCODE_REROUTE_COUNT; printenv XIT_OPENCODE_TURN_KEY; printenv XIT_OPENCODE_SESSION_ID; printenv XIT_OPENCODE_USER_MESSAGE_ID; exit 0"), 0755)

	cmd := exec.Command(bin, "auto", "env")
	cmd.Env = append(os.Environ(),
		"PATH="+tmpPath,
		"XIT_ORIGINAL_ENV="+envScript,
		"XIT_HOME=",
		"XIT_NONINTERACTIVE=1",
		"XIT_ADAPTER=opencode",
		"XIT_OPENCODE_REROUTE_COUNT=3",
		"XIT_OPENCODE_TURN_KEY=turn-leak",
		"XIT_OPENCODE_SESSION_ID=session-leak",
		"XIT_OPENCODE_USER_MESSAGE_ID=message-leak",
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
	if strings.Contains(outStr, "turn-leak") {
		t.Errorf("OpenCode turn key leaked into child process env, got:\n%s", outStr)
	}
	if strings.Contains(outStr, "session-leak") || strings.Contains(outStr, "message-leak") {
		t.Errorf("OpenCode turn env leaked into child process env, got:\n%s", outStr)
	}
}

// TestFormatCodexAutoStatusLine locks in the 0.2.51 follow-up per-command
// visible-feedback line shown in Codex tool output: format, and that it
// carries only aggregate byte/token counts and the exit code — never a
// command, path, repo name, prompt, reply, install id, or secret.
func TestFormatCodexAutoStatusLine(t *testing.T) {
	line := formatCodexAutoStatusLine(49356, 4813, 44543, 0)
	if !strings.HasPrefix(line, "XiT · auto · ") {
		t.Fatalf("expected line to start with %q, got: %s", "XiT · auto · ", line)
	}
	if !strings.Contains(line, "KB") {
		t.Errorf("expected KB units in line, got: %s", line)
	}
	if !strings.Contains(line, "saved ~") {
		t.Errorf("expected a saved-tokens figure, got: %s", line)
	}
	if !strings.Contains(line, "exit 0") {
		t.Errorf("expected exit code in line, got: %s", line)
	}
	forbidden := []string{"/Users/", "/home/", "prompt", "reply", "install", "token=", "api_key", "secret"}
	lower := strings.ToLower(line)
	for _, f := range forbidden {
		if strings.Contains(lower, strings.ToLower(f)) {
			t.Errorf("status line must never contain %q, got: %s", f, line)
		}
	}
}

// TestFormatCodexAutoStatusLineLargeSavings checks the k-token formatting
// threshold matches the user-specified example ("saved ~10.9k tokens").
func TestFormatCodexAutoStatusLineLargeSavings(t *testing.T) {
	// savedBytes/4 = 10900 tokens -> "10.9k"
	line := formatCodexAutoStatusLine(49356, 4813, 43600, 0)
	if !strings.Contains(line, "10.9k tokens") {
		t.Errorf("expected %q in line, got: %s", "10.9k tokens", line)
	}
}

// TestBuildCodexToolOutputAppendsStatusLine ensures the per-command status
// line is present alongside the existing real result content, never
// replacing it, and never leaking the underlying command text (which
// buildCodexToolOutput never receives in the first place).
func TestBuildCodexToolOutputAppendsStatusLine(t *testing.T) {
	summary := &output.Summary{
		BodyLines:  []string{"FAIL  ./pkg  (0.01s)"},
		ExitCode:   1,
		Confidence: "high",
	}
	out := buildCodexToolOutput(summary, 49356, 4813, 44543, 1)
	if !strings.Contains(out, "FAIL  ./pkg  (0.01s)") {
		t.Errorf("expected real diagnostic content preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "XiT · auto ·") {
		t.Errorf("expected the per-command status line appended, got:\n%s", out)
	}
	if !strings.Contains(out, "exit 1") {
		t.Errorf("expected the real exit code surfaced in the status line, got:\n%s", out)
	}
}

// TestChatGPTSetupAutoInstallsAndIsIdempotent runs `xit chatgpt setup --auto`
// as a real subprocess against a throwaway project dir and fake HOME,
// verifying: first run installs the hook, backs up (skips backup when there
// was nothing to back up), and a second run is idempotent (no duplicate
// entries, same success path).
func TestChatGPTSetupAutoInstallsAndIsIdempotent(t *testing.T) {
	bin := buildXit(t)
	projectDir := t.TempDir()
	fakeHome := t.TempDir()

	run := func() (string, int) {
		cmd := exec.Command(bin, "chatgpt", "setup", "--auto")
		cmd.Dir = projectDir
		cleanEnv(cmd)
		cmd.Env = append(cmd.Env, "HOME="+fakeHome, "XIT_TELEMETRY=off")
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	out1, code1 := run()
	if code1 != 0 {
		t.Fatalf("first setup run failed (exit %d):\n%s", code1, out1)
	}
	if !strings.Contains(out1, "Hook installed (all 4 lifecycle events)") {
		t.Errorf("expected a fresh install message, got:\n%s", out1)
	}
	if !strings.Contains(out1, "Automatic mode:             enabled") {
		t.Errorf("expected automatic mode enabled in final status, got:\n%s", out1)
	}

	hooksPath := filepath.Join(projectDir, ".codex", "hooks.json")
	firstContent, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("expected hooks.json to exist after setup: %v", err)
	}

	out2, code2 := run()
	if code2 != 0 {
		t.Fatalf("second (idempotent) setup run failed (exit %d):\n%s", code2, out2)
	}
	if !strings.Contains(out2, "already installed and complete") {
		t.Errorf("expected idempotent re-run message, got:\n%s", out2)
	}
	secondContent, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatalf("expected hooks.json to still exist: %v", err)
	}
	if string(firstContent) != string(secondContent) {
		t.Error("re-running setup must not change hooks.json content when already installed")
	}
}

// TestChatGPTSetupAutoRefusesWhenDuplicateExists proves setup never silently
// creates a double-firing configuration: if a hook is already present in
// BOTH the project layer and the Codex user-level layer, it must refuse.
func TestChatGPTSetupAutoRefusesWhenDuplicateExists(t *testing.T) {
	bin := buildXit(t)
	projectDir := t.TempDir()
	fakeHome := t.TempDir()

	// Seed a duplicate: install the hook at the project layer directly on
	// disk, AND at the Codex user-level layer (fakeHome/.codex/hooks.json),
	// simulating a pre-existing broad install before setup ever runs.
	install := exec.Command(bin, "hook", "install", "codex", "--scope", "project", "--yes")
	install.Dir = projectDir
	cleanEnv(install)
	install.Env = append(install.Env, "HOME="+fakeHome, "XIT_TELEMETRY=off")
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("seeding project-level install failed: %v\n%s", err, out)
	}
	userHooksPath := filepath.Join(fakeHome, ".codex", "hooks.json")
	projectHooksData, err := os.ReadFile(filepath.Join(projectDir, ".codex", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(userHooksPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userHooksPath, projectHooksData, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "chatgpt", "setup", "--auto")
	cmd.Dir = projectDir
	cleanEnv(cmd)
	cmd.Env = append(cmd.Env, "HOME="+fakeHome, "XIT_TELEMETRY=off")
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Fatalf("expected setup to refuse (non-zero exit) when a duplicate hook exists, got exit 0:\n%s", out)
	}
	if !strings.Contains(string(out), "BLOCKED") {
		t.Errorf("expected a BLOCKED message, got:\n%s", out)
	}
}
