package codexhook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/filters"
)

// PreToolUseInput is the JSON payload Codex sends to a pre_tool_use hook.
type PreToolUseInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// BashInput is the tool_input for Bash tool calls.
type BashInput struct {
	Command string `json:"command"`
}

// HookOutput is the JSON response returned to Codex.
type HookOutput struct {
	Decision      string `json:"decision"`
	StatusMessage string `json:"statusMessage,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// RunHookCommand reads a PreToolUse payload from stdin and writes a
// fail-open JSON response to stdout. It logs events to
// ~/.xit/codex-hooks/events.jsonl.
func RunHookCommand(home string) error {
	logDir := filepath.Join(home, "codex-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		writeAllow()
		return nil
	}

	logPath := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		writeAllow()
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(os.Stdin)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
		logEvent(f, "error", "", "", "fail_open", "stdin read error: "+err.Error())
		writeAllow()
		return nil
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	cwd, _ := os.Getwd()

	var payload PreToolUseInput
	if err := json.Unmarshal(input, &payload); err != nil {
		logEvent(f, ts, "", "", "fail_open", "parse error: "+err.Error())
		writeAllow()
		return nil
	}

	if payload.ToolName != "Bash" {
		logEvent(f, ts, "", "", "passthrough", "not Bash tool")
		writeAllow()
		return nil
	}

	var bash BashInput
	if err := json.Unmarshal(payload.ToolInput, &bash); err != nil {
		logEvent(f, ts, "", "", "fail_open", "parse bash input: "+err.Error())
		writeAllow()
		return nil
	}

	orig := strings.TrimSpace(bash.Command)
	policy := filters.ClassifyPolicy(strings.Fields(orig))
	wasWrapped := strings.HasPrefix(orig, "xit auto ") || strings.HasPrefix(orig, "./xit auto ")

	var action, reason, recommended, statusMsg string
	switch policy {
	case "should_compress":
		if wasWrapped {
			action = "observe"
			reason = "command already wrapped"
			statusMsg = "吸T神功 · Codex observe"
		} else {
			action = "observe"
			reason = "command classified as should_compress"
			recommended = "xit auto " + orig
			statusMsg = "吸T神功 · 建议使用 xit auto"
		}
	case "should_passthrough":
		action = "observe"
		reason = "command classified as should_passthrough"
		statusMsg = "吸T神功 · Codex observe"
	default:
		action = "observe"
		reason = "command policy: needs_review"
		statusMsg = "吸T神功 · Codex observe"
	}

	logEventFull(f, ts, orig, recommended, action, reason, cwd)

	out := HookOutput{
		Decision:      "allow",
		StatusMessage: statusMsg,
		Reason:        reason,
	}
	data, _ := json.Marshal(out)
	fmt.Println(string(data))
	return nil
}

func writeAllow() {
	out := HookOutput{Decision: "allow"}
	data, _ := json.Marshal(out)
	fmt.Println(string(data))
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
