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
	Permission string `json:"permission"`
}

// RunHookCommand reads a beforeShellExecution payload from stdin, logs the event,
// and returns {"permission":"allow"} to signal fail-open observe-only behavior.
func RunHookCommand(home string) error {
	logDir := filepath.Join(home, "cursor-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		outputAllow()
		return nil
	}

	logPath := filepath.Join(logDir, "events.jsonl")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		outputAllow()
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
		outputAllow()
		return nil
	}

	ts := time.Now().UTC().Format(time.RFC3339)
	cwd, _ := os.Getwd()
	sessionID := os.Getenv("XIT_SESSION_ID")

	var payload BeforeShellExecutionInput
	if err := json.Unmarshal(input, &payload); err != nil {
		logEvent(f, ts, "", "", "fail_open", "parse error: "+err.Error(), cwd)
		outputAllow()
		return nil
	}

	orig := strings.TrimSpace(payload.Command)
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

	logEventFull(f, ts, orig, recommended, action, reason, cwd, sessionID, policy)
	outputAllow()
	return nil
}

func outputAllow() {
	fmt.Println(`{"permission":"allow"}`)
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
