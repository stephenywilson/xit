package kimihook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type HookEvent struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
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

func RunHookCommand(home string) error {
	logDir := filepath.Join(home, "kimi-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Println("{}")
		return nil
	}

	cfg, err := ReadHookConfig(home)
	if err != nil {
		fmt.Println("{}")
		return nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
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
	cwd, _ := os.Getwd()
	sessionID := os.Getenv("XIT_SESSION_ID")

	var event HookEvent
	if err := json.Unmarshal(input, &event); err != nil {
		logEvent(f, ts, cfg.Mode, "", "", "error_fail_open", "parse error: "+err.Error(), cwd, sessionID)
		fmt.Println("{}")
		return nil
	}

	if event.ToolName != "Bash" && event.ToolName != "Shell" {
		logEvent(f, ts, cfg.Mode, "", "", "passthrough", "not Bash/Shell tool", cwd, sessionID)
		fmt.Println("{}")
		return nil
	}

	var bash BashInput
	if err := json.Unmarshal(event.ToolInput, &bash); err != nil {
		logEvent(f, ts, cfg.Mode, "", "", "error_fail_open", "parse bash input: "+err.Error(), cwd, sessionID)
		fmt.Println("{}")
		return nil
	}

	shouldReroute, recommended := ShouldReroute(bash.Command)

	if cfg.Mode == "reroute" && shouldReroute {
		reason := BuildRerouteReason(bash.Command, recommended, cfg.StatusStyle)
		displayMsg := reason
		logEventWithDisplay(f, ts, cfg.Mode, bash.Command, recommended, "reroute", reason, displayMsg, cwd, sessionID)

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
		reason = "observe mode only"
	} else if shouldReroute {
		reason = "reroute not enabled"
	} else {
		reason = "command not in reroute list"
	}
	logEvent(f, ts, cfg.Mode, bash.Command, recommended, action, reason, cwd, sessionID)
	fmt.Println("{}")
	return nil
}

func BuildRerouteReason(command, recommended, style string) string {
	if style == "detailed" {
		return fmt.Sprintf("XiT intercepted a high-noise shell command:\n\n  %s\n\nRecommended rerun:\n\n  %s\n\nWhy:\n  This command usually produces long terminal output. XiT can summarize the noise, preserve raw_log, and track context savings.\n\nMode:\n  Kimi safe reroute - no command rewrite - fail-open", command, recommended)
	}
	// compact (default)
	return fmt.Sprintf("XiT: reroute high-noise command. Run: %s", recommended)
}

func logEvent(f *os.File, ts, mode, original, recommended, action, reason, cwd, sessionID string) {
	logEventWithDisplay(f, ts, mode, original, recommended, action, reason, "", cwd, sessionID)
}

func logEventWithDisplay(f *os.File, ts, mode, original, recommended, action, reason, displayMsg, cwd, sessionID string) {
	rec := map[string]interface{}{
		"time":                ts,
		"mode":                mode,
		"original_command":    original,
		"recommended_command": recommended,
		"action":              action,
		"reason":              reason,
		"cwd":                 cwd,
	}
	if displayMsg != "" {
		rec["display_message"] = displayMsg
	}
	if sessionID != "" {
		rec["session_id"] = sessionID
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
}
