package claudehook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/vscodebridge"
)

// HookEvent mirrors Claude Code's PreToolUse hook JSON payload. cwd and
// session_id are read directly from the payload (matching how
// internal/codexhook resolves its own per-project home from in.Cwd) — not
// from os.Getwd()/an env var — so this hook resolves the SAME workspace
// Claude Code is actually operating in, regardless of host process or
// surface (CLI terminal vs. the VS Code Claude Code panel).
type HookEvent struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
	SessionID string          `json:"session_id"`
}

type BashInput struct {
	Command string `json:"command"`
}

type DenyResponse struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// resolveClaudeHome mirrors internal/codexhook's resolveCodexHome: an
// explicit XIT_HOME always wins; otherwise prefer the hook payload's own
// cwd (the project Claude Code is actually working in) over a fixed
// "always ~/.xit" fallback. Before this fix, RunHookCommand always used the
// caller-supplied fallback (~/.xit), so a project-local
// .xit/claude-hooks/config.json (e.g. "mode": "reroute") was silently never
// read — only the global ~/.xit one ever applied, regardless of which
// project's settings.json installed the hook.
func resolveClaudeHome(fallbackHome, payloadCwd string) string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	if payloadCwd == "" {
		return fallbackHome
	}
	return filepath.Join(payloadCwd, ".xit")
}

// isAlreadyWrapped reports whether cmd already invokes `xit auto` (directly
// or via "./xit auto"), so a command the AI wrote itself (per CLAUDE.md
// rules) or already retried after a reroute recommendation is never
// reroute-denied a second time.
func isAlreadyWrapped(cmd string) bool {
	c := strings.TrimSpace(cmd)
	return strings.HasPrefix(c, "xit auto ") || strings.HasPrefix(c, "./xit auto ")
}

func RunHookCommand(fallbackHome string) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
		fmt.Println("{}")
		return nil
	}

	var event HookEvent
	_ = json.Unmarshal(input, &event) // fail-open: zero-value event below if malformed

	// home resolution uses the payload's cwd verbatim (or fallbackHome if
	// absent) — exactly mirroring internal/codexhook's resolveCodexHome,
	// never guessing via os.Getwd(). The log's "cwd" field is best-effort
	// diagnostics only, so it can still fall back to the hook process's own
	// cwd when the payload omits one.
	home := resolveClaudeHome(fallbackHome, event.Cwd)
	logCwd := event.Cwd
	if logCwd == "" {
		logCwd, _ = os.Getwd()
	}
	sessionID := event.SessionID
	if sessionID == "" {
		sessionID = os.Getenv("XIT_SESSION_ID")
	}

	logDir := filepath.Join(home, "claude-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Println("{}")
		return nil
	}

	cfg, err := ReadHookConfig(home)
	if err != nil {
		fmt.Println("{}")
		return nil
	}

	logPath := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("{}")
		return nil
	}
	defer f.Close()

	ts := time.Now().UTC().Format(time.RFC3339)

	if len(input) == 0 {
		logEvent(f, ts, cfg.Mode, "", "", "error_fail_open", "empty stdin", logCwd, sessionID)
		fmt.Println("{}")
		return nil
	}
	if event.ToolName == "" {
		logEvent(f, ts, cfg.Mode, "", "", "error_fail_open", "parse error: missing tool_name", logCwd, sessionID)
		fmt.Println("{}")
		return nil
	}

	if event.ToolName != "Bash" {
		logEvent(f, ts, cfg.Mode, "", "", "passthrough", "not Bash tool", logCwd, sessionID)
		fmt.Println("{}")
		return nil
	}

	var bash BashInput
	if err := json.Unmarshal(event.ToolInput, &bash); err != nil {
		logEvent(f, ts, cfg.Mode, "", "", "error_fail_open", "parse bash input: "+err.Error(), logCwd, sessionID)
		fmt.Println("{}")
		return nil
	}

	// Bash tool calls that are already "xit auto ..." (the AI wrote it
	// itself per CLAUDE.md rules, or this is the retry after a reroute
	// recommendation below) are about to genuinely execute through xit.
	// This is the one observable signal available for the VS Code Claude
	// Code panel bridge: Claude Code's PreToolUse hook protocol has no
	// documented mechanism to mutate tool_input like Codex's hook does, so
	// XiT can never silently rewrite the command here — only recommend
	// (via deny, below) and observe once the AI has already wrapped it.
	if isAlreadyWrapped(bash.Command) && vscodebridge.IsVSCodeHost(vscodebridge.CurrentEnv()) {
		_, _ = vscodebridge.StartClaudeIfVSCode(home, logCwd, bash.Command, time.Now())
	}

	shouldReroute, recommended := ShouldReroute(bash.Command)

	if cfg.Mode == "reroute" && shouldReroute {
		// fail_open=true means "only deny if xit auto would actually run":
		// if it's missing, version-gate blocked, or the probe itself times
		// out/errors, let the original command through rather than steer
		// the AI toward a recommended replacement that can't execute
		// either — this is exactly the deadlock a stale/misconfigured
		// reroute config previously caused (deny -> "run xit auto" -> xit
		// auto also blocked -> no path forward).
		if cfg.FailOpen {
			if avail := checkXitAutoAvailable(); !avail.Available {
				reason := "reroute policy matched but xit auto is unavailable (" + avail.Reason + "); fail-open, allowing original command"
				logEvent(f, ts, cfg.Mode, bash.Command, recommended, "fail_open_allow:"+avail.Reason, reason, logCwd, sessionID)
				fmt.Println("{}")
				return nil
			}
		}

		reason := fmt.Sprintf("XiT recommends rerunning this high-output Bash command through XiT to reduce terminal noise and preserve raw logs. Please run: %s", recommended)
		logEvent(f, ts, cfg.Mode, bash.Command, recommended, "reroute", reason, logCwd, sessionID)

		resp := DenyResponse{}
		resp.HookSpecificOutput.HookEventName = "PreToolUse"
		resp.HookSpecificOutput.PermissionDecision = "deny"
		resp.HookSpecificOutput.PermissionDecisionReason = reason

		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
		return nil
	}

	action := "passthrough"
	reason := ""
	if cfg.Mode == "observe" && !shouldReroute {
		action = "observe"
	}
	if shouldReroute {
		reason = "reroute not enabled"
	} else {
		reason = "command not in reroute list"
	}
	logEvent(f, ts, cfg.Mode, bash.Command, recommended, action, reason, logCwd, sessionID)
	fmt.Println("{}")
	return nil
}

func logEvent(f *os.File, ts, mode, original, recommended, action, reason, cwd, sessionID string) {
	rec := map[string]interface{}{
		"time":                ts,
		"mode":                mode,
		"original_command":    original,
		"recommended_command": recommended,
		"action":              action,
		"reason":              reason,
		"cwd":                 cwd,
	}
	if sessionID != "" {
		rec["session_id"] = sessionID
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
}

func recommend(command string) string {
	c := strings.TrimSpace(command)
	prefixes := []string{"go test", "npm test", "pnpm test", "pytest", "cargo test", "git diff", "git log", "docker logs", "tsc", "eslint", "find", "grep", "rg", "npm install", "docker ps"}
	for _, p := range prefixes {
		if strings.HasPrefix(c, p) {
			return fmt.Sprintf("Consider running through XiT: xit --mode agent %s", command)
		}
	}
	return ""
}
