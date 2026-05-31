package codexhook

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/filters"
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
		"time":              ts,
		"original_command":  original,
		"recommended_command": recommended,
		"action":            action,
		"reason":            reason,
		"mode":              "observe",
	}
	if cwd != "" {
		rec["cwd"] = cwd
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
}
