package claudehook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		HookEventName              string `json:"hookEventName"`
		PermissionDecision         string `json:"permissionDecision"`
		PermissionDecisionReason   string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func RunHookCommand(home string) error {
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

	if event.ToolName != "Bash" {
		logEvent(f, ts, cfg.Mode, "", "", "passthrough", "not Bash tool", cwd, sessionID)
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
		reason := fmt.Sprintf("XiT recommends rerunning this high-output Bash command through XiT to reduce terminal noise and preserve raw logs. Please run: %s", recommended)
		logEvent(f, ts, cfg.Mode, bash.Command, recommended, "reroute", reason, cwd, sessionID)

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
	logEvent(f, ts, cfg.Mode, bash.Command, recommended, action, reason, cwd, sessionID)
	fmt.Println("{}")
	return nil
}

func logEvent(f *os.File, ts, mode, original, recommended, action, reason, cwd, sessionID string) {
	rec := map[string]interface{}{
		"time":              ts,
		"mode":              mode,
		"original_command":  original,
		"recommended_command": recommended,
		"action":            action,
		"reason":            reason,
		"cwd":               cwd,
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
