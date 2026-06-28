package codexhook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/filters"
	"github.com/stephenywilson/xit/internal/vscodebridge"
)

// PreToolUseInput is the JSON payload Codex sends to a PreToolUse hook.
type PreToolUseInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// BashInput is the tool_input for Bash tool calls.
type BashInput struct {
	Command string `json:"command"`
}

// RunHookCommand reads a PreToolUse payload from stdin, logs the event to
// ~/.xit/codex-hooks/events.jsonl, and exits silently (exit 0) to signal
// success without returning any unsupported JSON to Codex.
func RunHookCommand(home string) error {
	logDir := filepath.Join(home, "codex-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil
	}

	logPath := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(os.Stdin)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
		logEvent(f, "", "", "", "fail_open", "stdin read error: "+err.Error())
		return nil
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	cwd, _ := os.Getwd()

	var payload PreToolUseInput
	if err := json.Unmarshal(input, &payload); err != nil {
		logEvent(f, ts, "", "", "fail_open", "parse error: "+err.Error())
		return nil
	}

	if payload.ToolName != "Bash" {
		logEvent(f, ts, "", "", "passthrough", "not Bash tool")
		return nil
	}

	var bash BashInput
	if err := json.Unmarshal(payload.ToolInput, &bash); err != nil {
		logEvent(f, ts, "", "", "fail_open", "parse bash input: "+err.Error())
		return nil
	}

	orig := strings.TrimSpace(bash.Command)
	policy := filters.ClassifyPolicy(strings.Fields(orig))
	wasWrapped := strings.HasPrefix(orig, "xit auto ") || strings.HasPrefix(orig, "./xit auto ")

	var action, reason, recommended string
	switch policy {
	case "should_compress":
		if wasWrapped {
			action = "observe"
			reason = "command already wrapped"
		} else {
			action = "observe"
			reason = "command classified as should_compress"
			recommended = "xit auto " + orig
		}
	case "should_passthrough":
		action = "observe"
		reason = "command classified as should_passthrough"
	default:
		action = "observe"
		reason = "command policy: needs_review"
	}

	logEventFull(f, ts, orig, recommended, action, reason, cwd)
	return nil
}

func logEvent(f *os.File, ts, original, recommended, action, reason string) {
	logEventFull(f, ts, original, recommended, action, reason, "")
}

func logEventFull(f *os.File, ts, original, recommended, action, reason, cwd string) {
	rec := map[string]interface{}{
		"time":                ts,
		"original_command":    original,
		"recommended_command": recommended,
		"action":              action,
		"reason":              reason,
		"mode":                "observe",
	}
	if cwd != "" {
		rec["cwd"] = cwd
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
}

// ---------------------------------------------------------------------------
// Full Codex hook lifecycle: UserPromptSubmit / PreToolUse / PostToolUse / Stop
// ---------------------------------------------------------------------------
//
// One user prompt = one turn (session_id + turn_id). `xit auto` calls within
// the same turn accumulate into one turn-state file under
// state/codex-turns/<session>/<turn>.json; the two-line XiT footer is appended
// ONCE to the turn's final assistant answer, never to individual tool output.

// HookInput is the unified Codex hook payload (a superset of all event
// shapes; unused fields are simply absent/zero per the actual event).
type HookInput struct {
	SessionID        string          `json:"session_id"`
	TranscriptPath   string          `json:"transcript_path"`
	Cwd              string          `json:"cwd"`
	HookEventName    string          `json:"hook_event_name"`
	Model            string          `json:"model"`
	TurnID           string          `json:"turn_id"`
	Prompt           string          `json:"prompt"`
	ToolUseID        string          `json:"tool_use_id"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	ToolResponse     json.RawMessage `json:"tool_response"`
	StopHookActive   bool            `json:"stop_hook_active"`
	LastAssistantMsg string          `json:"last_assistant_message"`
}

func readHookInput() (HookInput, []byte) {
	data, _ := io.ReadAll(os.Stdin)
	var in HookInput
	_ = json.Unmarshal(data, &in)
	return in, data
}

// resolveCodexHome must resolve to the EXACT SAME directory `xit auto` itself
// uses, in every environment, or the turn-state lookup silently splits across
// two files (one hook handlers read/write, a different one `xit auto`
// accumulates into) and PostToolUse/Stop see a turn with run_count==0 even
// though `xit auto` really ran. `xit auto` resolves its home via xitHome():
// XIT_HOME env wins unconditionally, falling back to (process cwd)/.xit only
// when XIT_HOME is unset. So this MUST check XIT_HOME first too — preferring
// the payload's cwd over an explicitly-set XIT_HOME would silently diverge
// from `xit auto` (this was a real, confirmed bug: PostToolUse/Stop returned
// {} because they resolved to <payload cwd>/.xit while `xit auto` had written
// to $XIT_HOME, two different directories with the same session/turn id).
// Only when XIT_HOME is unset does the payload's cwd (most accurate to the
// project Codex considers active for this turn) become the right signal,
// since `xit auto` itself would then also fall back to its own process cwd —
// the same cwd Codex launched the Bash tool subprocess from.
func resolveCodexHome(fallbackHome, payloadCwd string) string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	if payloadCwd == "" {
		return fallbackHome
	}
	return filepath.Join(payloadCwd, ".xit")
}

// logTurnEvent appends a lifecycle event record to events.jsonl. Only safe
// metadata is recorded — never prompt text, tool_response content, or
// transcript paths' contents (the path itself is fine: it's a Codex-managed
// location reference, not the data).
func logTurnEvent(home, eventName, sessionID, turnID, note string) {
	logDir := filepath.Join(home, "codex-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(logDir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	rec := map[string]interface{}{
		"time":            time.Now().UTC().Format(time.RFC3339),
		"hook_event_name": eventName,
		"session_id":      sessionID,
		"turn_id":         turnID,
		"action":          "observe",
		"mode":            "turn_lifecycle",
	}
	if note != "" {
		rec["note"] = note
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
}

func writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Println("{}")
		return
	}
	fmt.Println(string(data))
}

// HandleUserPromptSubmit starts a new turn (session_id+turn_id), resetting
// run_count/saved_tokens_total to 0 — UNLESS the prompt carries
// FooterContinuationMarker (a Stop-triggered continuation of the turn that is
// still finishing, which must not be treated as a new user turn) or the
// turn_id is identical to the one already recorded (idempotent re-delivery).
// Never outputs visible text to the user and never blocks the prompt.
func HandleUserPromptSubmit(fallbackHome string) error {
	in, _ := readHookInput()
	home := resolveCodexHome(fallbackHome, in.Cwd)
	logTurnEvent(home, "UserPromptSubmit", in.SessionID, in.TurnID, "")
	if in.SessionID == "" {
		return nil
	}
	_, _ = ResetTurnForPrompt(home, in.SessionID, in.TurnID, in.Prompt)
	// Only a genuinely new user-initiated turn should flip the VS Code
	// Bridge into "正在守护" (thinking) — our own footer-continuation
	// re-submission (see FooterContinuationMarker) is not a real new turn
	// and must never flash "thinking" right as the final result is about
	// to be shown.
	if !strings.Contains(in.Prompt, FooterContinuationMarker) && vscodebridge.IsVSCodeHost(vscodebridge.CurrentEnv()) {
		_ = vscodebridge.StartTurnIfVSCode(home, in.Cwd, vscodebridge.Adapter, vscodebridge.Surface, time.Now())
	}
	return nil
}

// bashCommand extracts tool_input.command for a Bash tool call.
func bashCommand(toolInput json.RawMessage) string {
	var b struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(toolInput, &b) != nil {
		return ""
	}
	return b.Command
}

// HandlePreToolUse injects XIT_ADAPTER=codex + the real session_id/turn_id
// into any Bash command that already runs (or, if high-noise per the same
// classifier other adapters use, should be rerouted to run) `xit auto`, via
// the official command-rewrite output. This is the PRIMARY path for turn
// identity — process-ancestor detection in `xit auto` itself is only a
// fallback for when hooks are not installed/trusted.
func HandlePreToolUse(fallbackHome string) error {
	in, _ := readHookInput()
	home := resolveCodexHome(fallbackHome, in.Cwd)
	logTurnEvent(home, "PreToolUse", in.SessionID, in.TurnID, in.ToolName)
	if in.ToolName != "Bash" || in.SessionID == "" {
		return nil
	}
	cmd := bashCommand(in.ToolInput)
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	// Only generate (and later inject) a bridge run id when VSCODE_PID is
	// ambiently present — i.e. this hook process is somewhere inside VS
	// Code's process tree. Generating it unconditionally would leak a
	// useless XIT_VSCODE_BRIDGE_RUN_ID into every plain-terminal Codex CLI
	// user's visible rewritten command for no reason.
	var bridgeRunID string
	if vscodebridge.IsCodexVSCode(vscodebridge.CurrentEnv()) {
		bridgeRunID = vscodebridge.NewRunID()
	}
	rewritten, changed := RewriteCommandForTurn(cmd, in.SessionID, in.TurnID, bridgeRunID)
	if !changed {
		return nil
	}
	if bridgeRunID != "" {
		_, _ = vscodebridge.StartIfCodexVSCode(home, in.Cwd, cmd, in.SessionID, bridgeRunID, time.Now())
	}
	writeJSON(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "allow",
			"updatedInput":       map[string]interface{}{"command": rewritten},
		},
	})
	return nil
}

// HandlePostToolUse intentionally emits no stdout. Codex displays
// hookSpecificOutput.additionalContext in the UI, which violates XiT's
// Stop-only footer contract. Keep the hook as an observe-only lifecycle event
// for compatibility/future use, but never return visible hook context here.
func HandlePostToolUse(fallbackHome string) error {
	in, _ := readHookInput()
	home := resolveCodexHome(fallbackHome, in.Cwd)
	logTurnEvent(home, "PostToolUse", in.SessionID, in.TurnID, in.ToolName)
	return nil
}

// HandleStop guarantees the turn's final answer contains the footer exactly
// once when the turn had real `xit auto` activity:
//   - run_count==0                          -> allow, no footer.
//   - footer already in last_assistant_message -> allow, cleanup turn state.
//   - footer missing, first attempt (not stop_hook_active, not yet requested)
//     -> block once with a continuation reason carrying the exact footer text.
//   - otherwise (stop_hook_active==true, or footer already requested once)
//     -> NEVER block again (loop prevention); fail-open allow + cleanup, and
//     log a diagnostic note if the footer still never appeared.
func HandleStop(fallbackHome string) error {
	in, _ := readHookInput()
	home := resolveCodexHome(fallbackHome, in.Cwd)
	logTurnEvent(home, "Stop", in.SessionID, in.TurnID, "")
	if in.SessionID == "" {
		writeJSON(map[string]interface{}{})
		return nil
	}
	st := ReadTurnState(home, in.SessionID, in.TurnID)
	if st == nil || st.RunCount == 0 {
		_ = CleanupTurnState(home, in.SessionID, in.TurnID)
		finishVSCodeTurn(home, in.Cwd)
		writeJSON(map[string]interface{}{})
		return nil
	}
	if LastMessageHasFooter(st, in.LastAssistantMsg) {
		_ = CleanupTurnState(home, in.SessionID, st.TurnID)
		// The footer is confirmed in the final assistant message — this IS
		// "AI final answer done", the real signal the VS Code Bridge state
		// machine waits for before promoting a held run.finished result to
		// its final "本次省"/"执行失败" display.
		finishVSCodeTurn(home, in.Cwd)
		writeJSON(map[string]interface{}{})
		return nil
	}
	if in.StopHookActive || st.FooterContinuationUsed {
		// Loop prevention: never block a second time. If the footer still
		// never showed up, fail open and record a diagnostic note instead of
		// looping forever. From the VS Code Bridge's perspective this also
		// ends the turn — otherwise "收工中" would be stuck forever.
		logTurnEvent(home, "Stop", in.SessionID, in.TurnID, "footer_missing_after_continuation_fail_open")
		_ = CleanupTurnState(home, in.SessionID, st.TurnID)
		finishVSCodeTurn(home, in.Cwd)
		writeJSON(map[string]interface{}{})
		return nil
	}
	line1, line2 := BuildFooterLines(st)
	_ = MarkFooterContinuationUsed(home, in.SessionID, st.TurnID)
	// FooterContinuationMarker is zero-width (invisible). The remaining text
	// is framed as a short passive note ("XiT 已追加...") rather than an
	// imperative internal instruction, so if a user glances at the transient
	// continuation step it reads like a normal status line, not a leaked
	// system prompt. The trailing parenthetical is the minimum signal Codex
	// still needs to append the footer exactly once and avoid another tool
	// call.
	reason := FooterContinuationMarker + "XiT 已追加本轮 Token 节省摘要：\n\n" + line1 + "\n" + line2 + "\n\n（仅追加一次，不要再调用工具）"
	writeJSON(map[string]interface{}{
		"decision": "block",
		"reason":   reason,
	})
	return nil
}

// finishVSCodeTurn emits turn.finished when this Stop call genuinely ends
// the turn (footer confirmed, no real activity, or loop-prevention
// fail-open) — never on the "block, continue" path, where the turn is not
// actually over yet.
func finishVSCodeTurn(home, cwd string) {
	if vscodebridge.IsVSCodeHost(vscodebridge.CurrentEnv()) {
		_ = vscodebridge.FinishTurnIfVSCode(home, cwd, vscodebridge.Adapter, vscodebridge.Surface, time.Now())
	}
}
