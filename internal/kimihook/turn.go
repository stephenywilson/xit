package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TurnState represents the Kimi turn lifecycle state.
type TurnState struct {
	Status      string `json:"status"`
	Event       string `json:"event"`
	StartedAt   string `json:"started_at,omitempty"`
	FinishedAt  string `json:"finished_at,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	PromptChars int    `json:"prompt_chars,omitempty"`
}

// RunTurnHookCommand reads a Kimi lifecycle hook payload from stdin and writes
// turn state to .xit/state/turn.json. It is fail-open: any error outputs {}.
// args may contain an explicit event name (e.g. ["UserPromptSubmit"]).
func RunTurnHookCommand(home string, args []string) error {
	// Read stdin fully.
	var buf []byte
	s := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(s)
		if n > 0 {
			buf = append(buf, s[:n]...)
		}
		if err != nil {
			break
		}
	}
	data := buf

	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		fmt.Println("{}")
		return nil
	}

	// Extract event name with priority:
	// 1. explicit argv event
	// 2. JSON hookEventName
	// 3. JSON event
	// 4. JSON type
	// 5. JSON name
	// 6. fallback "active"
	eventName := ""
	if len(args) > 0 && args[0] != "" {
		eventName = args[0]
	}
	if eventName == "" {
		for _, key := range []string{"hookEventName", "event", "type", "name"} {
			if v, ok := payload[key].(string); ok && v != "" {
				eventName = v
				break
			}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	cwd, _ := os.Getwd()
	sessionID := os.Getenv("XIT_SESSION_ID")
	if sessionID == "" {
		if v, ok := payload["session_id"].(string); ok {
			sessionID = v
		}
	}
	if cwd == "" {
		if v, ok := payload["cwd"].(string); ok {
			cwd = v
		}
	}

	state := TurnState{
		Event:     eventName,
		SessionID: sessionID,
		Cwd:       cwd,
	}

	switch eventName {
	case "UserPromptSubmit":
		state.Status = "thinking"
		state.StartedAt = now
		if v, ok := payload["prompt"].(string); ok {
			state.PromptChars = len(v)
		}
	case "Stop":
		state.Status = "turn_completed"
		state.FinishedAt = now
		// Preserve started_at from existing turn.json if available.
		existing := readTurnStateForCwd(cwd)
		if existing.StartedAt != "" {
			state.StartedAt = existing.StartedAt
		}
	case "SessionStart":
		state.Status = "session_started"
		state.StartedAt = now
	case "SessionEnd":
		state.Status = "session_ended"
		state.FinishedAt = now
	default:
		// Unknown event: write a generic active state.
		state.Status = "active"
		state.StartedAt = now
	}

	// Determine project-first state home.
	stateHome := resolveTurnStateWriteHome(cwd)

	// Write state file (fail-open).
	_ = writeTurnState(stateHome, state)

	// Optional: append to event log.
	_ = appendTurnEvent(home, eventName, state, stateHome)

	fmt.Println("{}")
	return nil
}

func resolveTurnStateWriteHome(cwd string) string {
	projectHome, userHome := ResolveTurnStateHome(cwd)
	// If the project .xit directory exists, prefer it.
	if _, err := os.Stat(projectHome); err == nil {
		return projectHome
	}
	return userHome
}

func readTurnStateForCwd(cwd string) TurnState {
	projectHome, userHome := ResolveTurnStateHome(cwd)
	// Try project first, then user home.
	for _, home := range []string{projectHome, userHome} {
		path := filepath.Join(home, "state", "turn.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s TurnState
		if err := json.Unmarshal(data, &s); err == nil {
			return s
		}
	}
	return TurnState{}
}

func readTurnState(home string) TurnState {
	path := filepath.Join(home, "state", "turn.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return TurnState{}
	}
	var s TurnState
	_ = json.Unmarshal(data, &s)
	return s
}

func writeTurnState(home string, state TurnState) error {
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, "turn.json"), data, 0644)
}

func appendTurnEvent(home, eventName string, state TurnState, stateHome string) error {
	logDir := filepath.Join(home, "kimi-hooks")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "turn-events.jsonl")
	rec := map[string]interface{}{
		"time":       time.Now().UTC().Format(time.RFC3339),
		"event":      eventName,
		"status":     state.Status,
		"session_id": state.SessionID,
		"cwd":        state.Cwd,
		"state_file": filepath.Join(stateHome, "state", "turn.json"),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, string(data))
	return err
}
