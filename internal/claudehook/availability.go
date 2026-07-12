package claudehook

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/stephenywilson/xit/internal/updatecheck"
)

// cliVersion is the running xit binary's own version, set once by cmd/xit's
// main() via SetCLIVersion before any hook command executes. Hook commands
// run inside the very same binary `xit auto` does, so the fail-open
// availability probe below reads the on-disk version-check cache directly,
// in-process, rather than shelling out — no subprocess, no way to hang on a
// broken PATH, and it reuses the exact gate logic `xit auto` itself uses
// (internal/updatecheck.Result.ShouldBlockCorePath, the same one that
// produces the "this version is blocked" error cmdAuto prints).
var cliVersion = "0.0.0"

// SetCLIVersion records the running binary's version for the availability
// probe. Call once at startup; the zero-value default is safe (it simply
// never compares as "at or above minimum", the conservative direction).
func SetCLIVersion(v string) {
	if v != "" {
		cliVersion = v
	}
}

// availabilityProbeTimeout bounds how long the fail-open probe may block.
// exec.LookPath can hang on a pathological PATH entry (e.g. an unresponsive
// network mount), and a PreToolUse hook must never stall the user's
// terminal indefinitely.
const availabilityProbeTimeout = 200 * time.Millisecond

// xitAvailability is the fail-open probe's structured result — never just a
// bool, so a fail-open allow can log an accurate, non-sensitive reason.
type xitAvailability struct {
	Available bool
	Reason    string // "available", "xit_not_found", "xit_auto_blocked", "probe_error", "probe_timeout"
}

// checkXitAutoAvailable is package-level so tests can substitute a fake
// prober via dependency injection, without executing real subprocesses or
// depending on the test machine's actual XiT installation/version-check
// cache state.
var checkXitAutoAvailable = defaultCheckXitAutoAvailable

// xitAvailabilityWork is the actual probe work, wrapped by
// defaultCheckXitAutoAvailable's timeout/recover logic below. Broken out
// into its own swappable var so that wrapper can be tested directly
// (simulating a hang or an internal panic) without waiting on or faking the
// real exec.LookPath / on-disk cache behavior.
var xitAvailabilityWork = probeXitAutoAvailable

// defaultCheckXitAutoAvailable reports whether `xit auto <cmd>`, run as a
// fresh subprocess right now, would actually execute — not just whether a
// `xit` binary exists or `xit --version` succeeds (production 0.2.50 proved
// both of those can be true while `xit auto` itself is still version-gate
// blocked). It never runs the caller's original command. Fail-open: any
// timeout or internal error is reported as unavailable, never left hanging
// or propagated as a panic.
func defaultCheckXitAutoAvailable() xitAvailability {
	resultCh := make(chan xitAvailability, 1)
	go func() {
		defer func() {
			if recover() != nil {
				resultCh <- xitAvailability{Reason: "probe_error"}
			}
		}()
		resultCh <- xitAvailabilityWork()
	}()
	select {
	case res := <-resultCh:
		return res
	case <-time.After(availabilityProbeTimeout):
		return xitAvailability{Reason: "probe_timeout"}
	}
}

// userXitHome mirrors cmd/xit/main.go's userXiTHome() exactly (XIT_HOME env
// wins, else $HOME/.xit): the version-check cache `xit auto` reads is
// always keyed off the USER home, never the project-local .xit this hook
// otherwise resolves per-project (see resolveClaudeHome) — so the probe
// must look in the same place `cmdAuto`'s versionGate does, regardless of
// which project's hook invoked it.
func userXitHome() string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".xit")
	}
	return filepath.Join(home, ".xit")
}

// probeXitAutoAvailable does the real, synchronous availability check: is
// `xit` resolvable at all, and if so, does the SAME version-check cache
// `xit auto` itself consults show it as blocked. This never spawns a
// subprocess — hook commands execute inside the same binary `xit auto`
// does, so the gate is evaluated directly, in-process.
func probeXitAutoAvailable() xitAvailability {
	if _, err := exec.LookPath("xit"); err != nil {
		return xitAvailability{Reason: "xit_not_found"}
	}
	gate := updatecheck.NewClient(userXitHome(), cliVersion).CheckCachedOnly()
	if gate.ShouldBlockCorePath() {
		return xitAvailability{Reason: "xit_auto_blocked"}
	}
	return xitAvailability{Available: true, Reason: "available"}
}
