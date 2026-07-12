package claudehook

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
// duration of a test, so hookcmd-level behavior can be tested without
// executing real subprocesses or depending on this machine's actual XiT
// installation/version-check cache state.
func withFakeAvailability(t *testing.T, result xitAvailability) {
	t.Helper()
	orig := checkXitAutoAvailable
	checkXitAutoAvailable = func() xitAvailability { return result }
	t.Cleanup(func() { checkXitAutoAvailable = orig })
}

// --- hookcmd-level behavior (RunHookCommand) ---------------------------------

func TestObserveModeNeverDeniesEvenWhenPolicyMatches(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "observe", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) in observe mode even though policy matches, got: %s", out)
	}
}

func TestRerouteFailOpenFalseStillDeniesRegardlessOfAvailability(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: false}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, home, payload)
	hso := denyDecision(t, out)
	if hso == nil {
		t.Fatalf("expected strict deny with fail_open=false regardless of availability, got: %s", out)
	}
}

func TestRerouteFailOpenTrueXitAvailableStillDenies(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: true, Reason: "available"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, home, payload)
	hso := denyDecision(t, out)
	if hso == nil {
		t.Fatalf("expected deny when xit auto is available, got: %s", out)
	}
	reason, _ := hso["permissionDecisionReason"].(string)
	if !strings.Contains(reason, "xit auto find . -maxdepth 4 -type f") {
		t.Errorf("expected recommended command in reason, got: %q", reason)
	}
}

func TestRerouteFailOpenTrueXitMissingAllows(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) when xit is missing, got: %s", out)
	}
}

func TestRerouteFailOpenTrueXitBlockedAllows(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_auto_blocked"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	out := runHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow ({}) when xit auto is version-blocked, got: %s", out)
	}
}

func TestRerouteFailOpenTrueProbeTimeoutOrErrorAllows(t *testing.T) {
	for _, reason := range []string{"probe_timeout", "probe_error"} {
		t.Run(reason, func(t *testing.T) {
			withFakeAvailability(t, xitAvailability{Available: false, Reason: reason})
			home := filepath.Join(t.TempDir(), ".xit")
			if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
				t.Fatal(err)
			}
			payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
			out := runHookCommand(t, home, payload)
			if strings.TrimSpace(out) != "{}" {
				t.Fatalf("expected allow ({}) on %s, got: %s", reason, out)
			}
		})
	}
}

func TestRerouteFailOpenAllowLogsAccurateReason(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_auto_blocked"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"find . -maxdepth 4 -type f"}}`
	runHookCommand(t, home, payload)

	data, err := os.ReadFile(filepath.Join(home, "claude-hooks", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "xit_auto_blocked") {
		t.Errorf("expected fail-open reason in log, got:\n%s", data)
	}
	if !strings.Contains(string(data), "fail_open") {
		t.Errorf("expected fail_open action marker in log, got:\n%s", data)
	}
	// The log must still show the reroute policy actually matched.
	if !strings.Contains(string(data), "find . -maxdepth 4 -type f") {
		t.Errorf("expected original command preserved in log, got:\n%s", data)
	}
}

func TestNonMatchingCommandUnaffectedByFailOpen(t *testing.T) {
	withFakeAvailability(t, xitAvailability{Available: false, Reason: "xit_not_found"})
	home := filepath.Join(t.TempDir(), ".xit")
	if err := WriteHookConfig(home, &HookConfig{Mode: "reroute", FailOpen: true}); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git status --short"}}`
	out := runHookCommand(t, home, payload)
	if strings.TrimSpace(out) != "{}" {
		t.Fatalf("expected allow for non-matching command regardless of availability, got: %s", out)
	}
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

// --- probeXitAutoAvailable (real, no-subprocess, no-fake logic) -------------
//
// probeXitAutoAvailable always reads the version-check cache from the USER
// xit home (XIT_HOME env, or $HOME/.xit) — the same place `xit auto` itself
// reads/writes it (cmd/xit/main.go's userXiTHome/versionGate) — never a
// project-local .xit, regardless of which project's hook invoked it. Tests
// below control that via XIT_HOME.

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

func TestProbeXitAutoAvailableNoCacheIsAvailable(t *testing.T) {
	binDir := fakeExecutableOnPath(t, "xit")
	t.Setenv("PATH", binDir)
	t.Setenv("XIT_HOME", t.TempDir()) // no version-check.json => updatecheck fails open

	got := probeXitAutoAvailable()
	if !got.Available {
		t.Fatalf("expected available with no version-check cache (fail-open), got %+v", got)
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

func TestProbeXitAutoAvailableEqualToMinimumIsAvailable(t *testing.T) {
	// Regression guard mirroring the real v0.2.51 fix this task builds on:
	// current == min_cli must never be treated as blocked, even if the
	// server naively said "blocked".
	binDir := fakeExecutableOnPath(t, "xit")
	t.Setenv("PATH", binDir)
	userHome := t.TempDir()
	t.Setenv("XIT_HOME", userHome)
	writeVersionCache(t, userHome, updatecheck.VersionInfo{
		LatestCLI: "0.2.51",
		MinCLI:    "0.2.50",
		Severity:  "required",
		FetchedAt: time.Now(),
	})
	origVersion := cliVersion
	SetCLIVersion("0.2.51")
	t.Cleanup(func() { cliVersion = origVersion })

	got := probeXitAutoAvailable()
	if !got.Available {
		t.Fatalf("expected available when current >= min_cli, got %+v", got)
	}
}

func TestProbeXitAutoAvailableIgnoresProjectLocalHome(t *testing.T) {
	// Regression test for the bug this task's verification pass caught: the
	// probe must key off the USER xit home, not whatever project-local .xit
	// a caller happens to be resolving for the hook's own event log. Plant a
	// blocked cache ONLY in a project-local dir and confirm it's ignored.
	binDir := fakeExecutableOnPath(t, "xit")
	t.Setenv("PATH", binDir)
	userHome := t.TempDir() // left empty: no cache => fail-open available
	t.Setenv("XIT_HOME", userHome)

	projectLocalHome := filepath.Join(t.TempDir(), ".xit")
	writeVersionCache(t, projectLocalHome, updatecheck.VersionInfo{
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
		t.Fatalf("expected available: a project-local cache must never affect the probe, got %+v", got)
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

func TestSetCLIVersionIgnoresEmptyString(t *testing.T) {
	origVersion := cliVersion
	t.Cleanup(func() { cliVersion = origVersion })

	SetCLIVersion("1.2.3")
	SetCLIVersion("")
	if cliVersion != "1.2.3" {
		t.Errorf("expected SetCLIVersion(\"\") to be a no-op, got %q", cliVersion)
	}
}
