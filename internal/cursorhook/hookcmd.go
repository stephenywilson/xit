package cursorhook

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

// BeforeShellExecutionInput is the JSON payload Cursor sends to beforeShellExecution.
type BeforeShellExecutionInput struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	Sandbox bool   `json:"sandbox"`
}

// BeforeShellExecutionOutput is the JSON response Cursor expects.
type BeforeShellExecutionOutput struct {
	Permission  string `json:"permission"`
	UserMessage string `json:"user_message,omitempty"`
	AgentMessage string `json:"agent_message,omitempty"`
}

// RunHookCommand reads a beforeShellExecution payload from stdin, logs the event,
// and returns permission based on mode (observe = allow, strict = ask for missed compress).
func RunHookCommand(home string) error {
	logDir := filepath.Join(home, "cursor-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		outputResult("allow", "", "")
		return nil
	}

	logPath := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		outputResult("allow", "", "")
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(os.Stdin)
	var input []byte
	for scanner.Scan() {
		input = append(input, scanner.Bytes()...)
	}
	if err := scanner.Err(); err != nil {
		logEvent(f, "", "", "", "fail_open", "stdin read error: "+err.Error(), "")
		outputResult("allow", "", "")
		return nil
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	cwd, _ := os.Getwd()
	sessionID := os.Getenv("XIT_SESSION_ID")

	var payload BeforeShellExecutionInput
	if err := json.Unmarshal(input, &payload); err != nil {
		logEvent(f, ts, "", "", "fail_open", "parse error: "+err.Error(), cwd)
		outputResult("allow", "", "")
		return nil
	}

	orig := strings.TrimSpace(payload.Command)
	policy := filters.ClassifyPolicy(strings.Fields(orig))
	wasWrapped := strings.HasPrefix(orig, "xit auto ") || strings.HasPrefix(orig, "./xit auto ")

	hookCfg, _ := ReadHookConfig(home)
	mode := hookCfg.Mode
	if mode == "" {
		mode = "observe"
	}

	var action, reason, recommended, permission string
	var userMsg, agentMsg string

	switch policy {
	case "should_compress":
		if wasWrapped {
			action = "observe"
			reason = "command already wrapped"
			permission = "allow"
		} else {
			recommended = "xit auto " + orig
			if mode == "strict" {
				action = "ask"
				reason = "strict mode: high-output command not wrapped"
				permission = "ask"
				userMsg = fmt.Sprintf("XiT: This command usually produces long output. Use '%s' to compress and save tokens.", recommended)
				agentMsg = fmt.Sprintf("Consider using %s to reduce context noise.", recommended)
			} else {
				action = "observe"
				reason = "command classified as should_compress"
				permission = "allow"
			}
		}
	case "should_passthrough":
		action = "observe"
		reason = "command classified as should_passthrough"
		permission = "allow"
	default:
		action = "observe"
		reason = "command policy: needs_review"
		permission = "allow"
	}

	logEventFull(f, ts, orig, recommended, action, reason, cwd, sessionID, policy)
	outputResult(permission, userMsg, agentMsg)
	return nil
}

func outputResult(permission, userMsg, agentMsg string) {
	out := BeforeShellExecutionOutput{
		Permission:   permission,
		UserMessage:  userMsg,
		AgentMessage: agentMsg,
	}
	data, _ := json.Marshal(out)
	fmt.Println(string(data))
}

func logEvent(f *os.File, ts, original, recommended, action, reason, cwd string) {
	logEventFull(f, ts, original, recommended, action, reason, cwd, "", "")
}

func logEventFull(f *os.File, ts, original, recommended, action, reason, cwd, sessionID, policy string) {
	rec := map[string]interface{}{
		"time":                ts,
		"adapter":             "cursor",
		"event":               "beforeShellExecution",
		"original_command":    original,
		"recommended_command": recommended,
		"action":              action,
		"reason":              reason,
		"policy":              policy,
		"cwd":                 cwd,
	}
	if sessionID != "" {
		rec["session_id"] = sessionID
	}
	data, _ := json.Marshal(rec)
	f.WriteString(string(data) + "\n")
}
