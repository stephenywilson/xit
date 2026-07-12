package kimihook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stephenywilson/xit/internal/updatecheck"
)

// withFakeAvailability swaps the package-level availability prober for the
// duration of a test, mirroring internal/claudehook's test helper of the
// same shape.
func withFakeAvailability(t *testing.T, result xitAvailability) {
	t.Helper()
	orig := checkXitAutoAvailable
	checkXitAutoAvailable = func() xitAvailability { return result }
	t.Cleanup(func() { checkXitAutoAvailable = orig })
}

func runKimiHookCommand(t *testing.T, home, payload string) string {
	t.Helper()
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
		t.Fatalf("RunHookCommand failed: %v", err)
	}

	outData := make([]byte, 4096)
	n, _ := pr.Read(outData)
	return string(outData[:n])
}

func denyDecision(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	hso, _ := resp["hookSpecificOutput"].(map[string]interface{})
	if hso == nil || hso["permissionDecision"] != "deny" {
		return nil
	}
	return hso
}

// --- hookcmd-level behavior (RunHookCommand) ---------------------------------

func TestObserveModeNeverDeniesEvenWhenPolicyMatches(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "observe", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	out := runKimiHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) in observe mode even though policy matches, got: %s", out)
	}
}

func TestRerouteFailOpenFalseStillDeniesRegardlessOfAvailability(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", RerouteEnabled: true, FailOpen: false}); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	out := runKimiHookCommand(t, home, payload)
	if denyDecision(t, out) == nil {
		t.Fatalf("expected strict deny with fail_open=false regardless of availability, got: %s", out)
	}
}

func TestRerouteFailOpenTrueXitAvailableStillDenies(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: true, Reason: "available"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", RerouteEnabled: true, FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	out := runKimiHookCommand(t, home, payload)
	hso := denyDecision(t, out)
	if hso == nil {
		t.Fatalf("expected deny when xit auto is available, got: %s", out)
	}
	reason, _ := hso["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "xit auto go test -v ./...") {
		t.Errorf("expected recommended command in reason, got: %q", reason)
	}
}

func TestRerouteFailOpenTrueXitMissingAllows(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", RerouteEnabled: true, FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	out := runKimiHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) when xit is missing, got: %s", out)
	}
}

func TestRerouteFailOpenTrueXitBlockedAllows(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_auto_blocked"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", RerouteEnabled: true, FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	out := runKimiHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) when xit auto is version-blocked, got: %s", out)
	}
}

func TestRerouteFailOpenAllowLogsAccurateReason(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_auto_blocked"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", RerouteEnabled: true, FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	runKimiHookCommand(t, home, payload)

	data, err := os.ReadFile(filepath.Join(home, "kimi-hooks", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "xit_auto_blocked") {
		t.Errorf("expected fail-open reason in log, got:\n%s", data)
	}
	if !strings.Contains(string(data), "fail_open") {
		t.Errorf("expected fail_open action marker in log, got:\n%s", data)
	}
}

// --- probeXitAutoAvailable (real, no-subprocess, no-fake logic) -------------
//
// probeXitAutoAvailable always reads the version-check cache from the USER
// xit home (XIT_HOME env, or $HOME/.xit) — the same place `xit auto` itself
// reads/writes it — never whatever home a caller passed to RunHookCommand.

func TestProbeXitAutoAvailableXitNotFound(t *testing.T) {
	emptyPathDir := t.TempDir()
	t.Setenv("PATH", emptyPathDir)
	t.Setenv("XIT_HOME", t.TempDir())

	got := probeXitAutoAvailable()
	if got.Available {
		t.Fatalf("expected unavailable when xit is not on PATH, got %+v", got)
	}
	if got.Reason != "xit_not_found" {
		t.Errorf("expected reason xit_not_found, got %q", got.Reason)
	}
}

func TestProbeXitAutoAvailableBlockedByVersionGate(t *testing.T) {
	binDir := fakeExecutableOnPath(t, "xit")
	t.Setenv("PATH", binDir)
	userHome := t.TempDir()
	t.Setenv("XIT_HOME", userHome)
	writeVersionCache(t, userHome, updatecheck.VersionInfo{
		LatestCLI: "0.2.51",
		MinCLI:    "0.2.50",
		Severity:  "blocked",
		FetchedAt: time.Now(),
	})
	origVersion := cliVersion
	SetCLIVersion("0.2.49") // genuinely below min_cli
	t.Cleanup(func() { cliVersion = origVersion })

	got := probeXitAutoAvailable()
	if got.Available {
		t.Fatalf("expected unavailable when xit auto is version-gate blocked, got %+v", got)
	}
	if got.Reason != "xit_auto_blocked" {
		t.Errorf("expected reason xit_auto_blocked, got %q", got.Reason)
	}
}

func TestProbeXitAutoAvailableIgnoresCallerHome(t *testing.T) {
	// Regression test: the probe must key off the USER xit home
	// (XIT_HOME/$HOME/.xit), never whatever home a caller happens to pass
	// to RunHookCommand for its own config/log resolution.
	binDir := fakeExecutableOnPath(t, "xit")
	t.Setenv("PATH", binDir)
	userHome := t.TempDir() // left empty: no cache => fail-open available
	t.Setenv("XIT_HOME", userHome)

	callerHome := filepath.Join(t.TempDir(), ".xit")
	writeVersionCache(t, callerHome, updatecheck.VersionInfo{
		LatestCLI: "0.2.51",
		MinCLI:    "0.2.50",
		Severity:  "blocked",
		FetchedAt: time.Now(),
	})
	origVersion := cliVersion
	SetCLIVersion("0.2.49")
	t.Cleanup(func() { cliVersion = origVersion })

	got := probeXitAutoAvailable()
	if !got.Available {
		t.Fatalf("expected available: a caller-local cache must never affect the probe, got %+v", got)
	}
}

func fakeExecutableOnPath(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeVersionCache(t *testing.T, home string, info updatecheck.VersionInfo) {
	t.Helper()
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "version-check.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// --- defaultCheckXitAutoAvailable: timeout + panic wrapper -------------------

func TestDefaultCheckXitAutoAvailableTimesOutOnSlowProbe(t *testing.T) {
	origWork := xitAvailabilityWork
	xitAvailabilityWork = func() xitAvailability {
		time.Sleep(availabilityProbeTimeout * 5)
		return xitAvailability{Available: true, Reason: "available"}
	}
	t.Cleanup(func() { xitAvailabilityWork = origWork })

	got := defaultCheckXitAutoAvailable()
	if got.Available {
		t.Fatalf("expected unavailable on timeout, got %+v", got)
	}
	if got.Reason != "probe_timeout" {
		t.Errorf("expected reason probe_timeout, got %q", got.Reason)
	}
}

func TestDefaultCheckXitAutoAvailableRecoversFromPanic(t *testing.T) {
	origWork := xitAvailabilityWork
	xitAvailabilityWork = func() xitAvailability {
		panic("boom")
	}
	t.Cleanup(func() { xitAvailabilityWork = origWork })

	got := defaultCheckXitAutoAvailable()
	if got.Available {
		t.Fatalf("expected unavailable on internal panic, got %+v", got)
	}
	if got.Reason != "probe_error" {
		t.Errorf("expected reason probe_error, got %q", got.Reason)
	}
}
