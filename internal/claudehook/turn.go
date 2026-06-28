package claudehook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/stephenywilson/xit/internal/vscodebridge"
)

// readTurnHookInput reads a Claude Code hook JSON payload from stdin and
// returns the parsed event plus the home directory resolved the same way
// RunHookCommand does (payload cwd, or XIT_HOME, or the caller's fallback).
// Fail-open: a malformed/empty payload yields a zero-value event rather
// than an error.
func readTurnHookInput(fallbackHome string) (HookEvent, string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	var event HookEvent
	_ = json.Unmarshal(input, &event)
	return event, resolveClaudeHome(fallbackHome, event.Cwd)
}

// HandleUserPromptSubmit emits a VS Code Bridge turn.started signal when
// running inside VS Code (VSCODE_PID ambiently present). Claude Code has no
// XiT-managed per-turn footer state to track here (unlike Codex's
// HandleStop) — this is intentionally minimal.
//
// Not installed by default (see install.go / docs/claude.md): whether the
// real VS Code Claude Code panel reliably triggers UserPromptSubmit is
// unconfirmed, so XiT does not silently add this hook to the user's
// settings.json. The capability exists and is tested so it can be enabled
// once that's verified.
func HandleUserPromptSubmit(fallbackHome string) error {
	event, home := readTurnHookInput(fallbackHome)
	if event.SessionID != "" && vscodebridge.IsVSCodeHost(vscodebridge.CurrentEnv()) {
		_ = vscodebridge.StartTurnIfVSCode(home, event.Cwd, vscodebridge.AdapterClaude, vscodebridge.SurfaceClaudeCode, time.Now())
	}
	fmt.Println("{}")
	return nil
}

// HandleStop emits a VS Code Bridge turn.finished signal when running
// inside VS Code. Same "not installed by default" caveat as
// HandleUserPromptSubmit above — see docs/claude.md.
func HandleStop(fallbackHome string) error {
	event, home := readTurnHookInput(fallbackHome)
	if event.SessionID != "" && vscodebridge.IsVSCodeHost(vscodebridge.CurrentEnv()) {
		_ = vscodebridge.FinishTurnIfVSCode(home, event.Cwd, vscodebridge.AdapterClaude, vscodebridge.SurfaceClaudeCode, time.Now())
	}
	fmt.Println("{}")
	return nil
}
