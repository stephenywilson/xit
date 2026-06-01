package autostate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AutoState mirrors the current-run/current.json structure written by cmdAuto.
type AutoState struct {
	SchemaVersion      int     `json:"schema_version"`
	Status             string  `json:"status"`
	Command            string  `json:"command"`
	SavedBytes         int64   `json:"saved_bytes"`
	RawBytes           int64   `json:"raw_bytes"`
	SummaryBytes       int64   `json:"summary_bytes"`
	SavedTokens        int64   `json:"saved_tokens"`
	SavedTokensDisplay string  `json:"saved_tokens_display"`
	EstimatedReduction float64 `json:"estimated_reduction"`
	RawLog             string  `json:"raw_log"`
	StartedAt          string  `json:"started_at"`
	HeartbeatAt        string  `json:"heartbeat_at"`
	CompletedAt        string  `json:"completed_at"`
	FinishedAt         string  `json:"finished_at"`
	ExitCode           int     `json:"exit_code"`
	PID                int     `json:"pid"`
}

// Read tries to read current-run.json/current.json from projectHome first, then falls back to userHome.
// It returns the parsed state, the path it was read from, and any error.
// If neither file exists or both are unreadable, it returns nil and the error.
func Read(projectHome, userHome string) (*AutoState, string, error) {
	if projectHome != "" {
		for _, name := range []string{"current-run.json", "current.json"} {
			path := filepath.Join(projectHome, "state", name)
			if state, err := readFile(path); err == nil {
				return state, path, nil
			}
		}
	}
	if userHome != "" {
		for _, name := range []string{"current-run.json", "current.json"} {
			path := filepath.Join(userHome, "state", name)
			if state, err := readFile(path); err == nil {
				return state, path, nil
			}
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

func parseFreshTime(primary, fallback string) (time.Time, error) {
	if primary != "" {
		return time.Parse(time.RFC3339, primary)
	}
	return time.Parse(time.RFC3339, fallback)
}

// IsRunningFresh returns true if the state is running and heartbeat is recent.
func IsRunningFresh(state *AutoState, now time.Time) bool {
	if state == nil || state.Status != "running" {
		return false
	}
	t, err := parseFreshTime(state.HeartbeatAt, state.StartedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < 15*time.Second
}

// IsCompletedFresh returns true if the state is completed and finished within the last 30 seconds.
func IsCompletedFresh(state *AutoState, now time.Time) bool {
	if state == nil || (state.Status != "completed" && state.Status != "failed") {
		return false
	}
	t, err := parseFreshTime(state.CompletedAt, state.FinishedAt)
	if err != nil {
		return false
	}
	return now.Sub(t) < 30*time.Second
}
