package autostate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AutoState mirrors the current.json structure written by cmdAuto.
type AutoState struct {
	Status     string `json:"status"`
	Command    string `json:"command"`
	SavedBytes int64  `json:"saved_bytes"`
	RawLog     string `json:"raw_log"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	ExitCode   int    `json:"exit_code"`
	PID        int    `json:"pid"`
}

// Read tries to read current.json from projectHome first, then falls back to userHome.
// It returns the parsed state, the path it was read from, and any error.
// If neither file exists or both are unreadable, it returns nil and the error.
func Read(projectHome, userHome string) (*AutoState, string, error) {
	if projectHome != "" {
		path := filepath.Join(projectHome, "state", "current.json")
		if state, err := readFile(path); err == nil {
			return state, path, nil
		}
	}
	if userHome != "" {
		path := filepath.Join(userHome, "state", "current.json")
		if state, err := readFile(path); err == nil {
			return state, path, nil
		}
	}
	return nil, "", os.ErrNotExist
}

func readFile(path string) (*AutoState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state AutoState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// IsRunningFresh returns true if the state is running and started within the last 10 minutes.
func IsRunningFresh(state *AutoState, now time.Time) bool {
	if state == nil || state.Status != "running" || state.StartedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, state.StartedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < 10*time.Minute
}

// IsCompletedFresh returns true if the state is completed and finished within the last 30 seconds.
func IsCompletedFresh(state *AutoState, now time.Time) bool {
	if state == nil || state.Status != "completed" || state.FinishedAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, state.FinishedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < 30*time.Second
}
