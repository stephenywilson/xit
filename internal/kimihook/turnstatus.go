package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TurnStats holds statistics scoped to the current turn.
type TurnStats struct {
	AutoCount   int `json:"auto_count"`
	SavedBytes  int `json:"saved_bytes"`
	SavedTokens int `json:"saved_tokens"`
}

// TurnStatusResult holds the parsed turn status for display.
type TurnStatusResult struct {
	StateFile         string      `json:"state_file"`
	FallbackStateFile string      `json:"fallback_state_file"`
	Source            string      `json:"source"`
	CurrentTurn       TurnState   `json:"current_turn"`
	AutoState         AutoState   `json:"auto_state"`
	TurnStats         TurnStats   `json:"turn_stats"`
	ToolbarExpected   string      `json:"toolbar_expected"`
}

// AutoState mirrors the current.json structure.
type AutoState struct {
	Status     string `json:"status"`
	Command    string `json:"command"`
	SavedBytes int    `json:"saved_bytes"`
	RawLog     string `json:"raw_log"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// ReadTurnStatus reads both turn.json and current.json and computes the
// expected toolbar text.
// It prefers project-scoped state ($PWD/.xit/state/turn.json) over user-scoped
// state (~/.xit/state/turn.json).
func ReadTurnStatus(home string) *TurnStatusResult {
	projectHome, userHome := ResolveTurnStateHome("")

	res := &TurnStatusResult{
		StateFile:         filepath.Join(projectHome, "state", "turn.json"),
		FallbackStateFile: filepath.Join(userHome, "state", "turn.json"),
		Source:            "none",
	}

	// Read turn.json - project first, then fallback to user home.
	turnPath := filepath.Join(projectHome, "state", "turn.json")
	if data, err := os.ReadFile(turnPath); err == nil {
		_ = json.Unmarshal(data, &res.CurrentTurn)
		res.Source = "project"
	} else {
		turnPath = filepath.Join(userHome, "state", "turn.json")
		if data, err := os.ReadFile(turnPath); err == nil {
			_ = json.Unmarshal(data, &res.CurrentTurn)
			res.Source = "user"
		}
	}

	// Read current.json - project first, then fallback to user home.
	currentPath := filepath.Join(projectHome, "state", "current.json")
	if data, err := os.ReadFile(currentPath); err == nil {
		_ = json.Unmarshal(data, &res.AutoState)
	} else {
		currentPath = filepath.Join(userHome, "state", "current.json")
		if data, err := os.ReadFile(currentPath); err == nil {
			_ = json.Unmarshal(data, &res.AutoState)
		}
	}

	res.TurnStats = computeTurnStats(home, res.CurrentTurn)
	res.ToolbarExpected = ComputeToolbarText(home, res.CurrentTurn, res.AutoState)
	return res
}

// FormatSavedTokens converts saved bytes to a token display string.
// saved_tokens = saved_bytes / 4
// <1000 tokens: "省{N} Token"
// >=1000 tokens: "省{round(N/1000)}k Token"
func FormatSavedTokens(savedBytes int) string {
	savedTokens := savedBytes / 4
	if savedTokens <= 0 {
		return ""
	}
	if savedTokens < 1000 {
		return fmt.Sprintf("省%d Token", savedTokens)
	}
	return fmt.Sprintf("省%dk Token", (savedTokens+500)/1000)
}

// ComputeToolbarText implements the visual turn state machine.
// Priority: auto_running > auto_completed_short > turn_active > turn_completed_result > session_result > ready
func ComputeToolbarText(home string, turn TurnState, auto AutoState) string {
	now := time.Now()

	// 1. auto_running (highest priority)
	if auto.Status == "running" && auto.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, auto.StartedAt); err == nil && now.Sub(t) < 10*time.Minute {
			return "吸T神功 · 正在吸T中"
		}
	}

	// 2. auto_completed_short (30s window)
	if auto.Status == "completed" && auto.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, auto.FinishedAt); err == nil && now.Sub(t) < 30*time.Second {
			tokenStr := FormatSavedTokens(auto.SavedBytes)
			if tokenStr != "" {
				return "吸T完成 · 本次" + tokenStr
			}
			return "吸T完成 · 已压缩输出"
		}
	}
	if auto.Status == "failed" && auto.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, auto.FinishedAt); err == nil && now.Sub(t) < 30*time.Second {
			return "吸T完成 · 已压缩输出"
		}
	}

	// 3. turn_active / guarding
	if turn.Status == "thinking" || turn.Status == "active" {
		return "吸T神功 · 守护你的T"
	}

	// Compute turn-scoped stats for lower-priority states
	turnAutoCount, turnSaved := computeTurnStatsValues(home, turn)

	// 4. turn_completed_result
	if turn.Status == "turn_completed" && turn.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, turn.FinishedAt); err == nil && now.Sub(t) < 60*time.Second {
			if turnAutoCount > 0 {
				// Representative text for turn_completed with auto records
				tokenStr := FormatSavedTokens(turnSaved)
				preview := fmt.Sprintf("本次吸T%d次 · %s", turnAutoCount, tokenStr)
				if displayWidthForTurn(preview) <= 32 {
					return preview
				}
				short := fmt.Sprintf("本次吸T%d次", turnAutoCount)
				if displayWidthForTurn(short) <= 32 {
					return short
				}
				return "吸T完成 · raw_log 已留证"
			}
			// No auto records: show positive completion text
			return "吸T神功 · 本次已守护"
		}
	}

	// 5. session_result (has auto records but no active turn / running / recent completed)
	// Note: in turn-scoped mode we no longer show session aggregate in toolbar.
	// Only show ready when there is no active turn.
	_ = turnSaved

	// 6. ready
	return "吸T神功 · 准备就绪"
}

func displayWidthForTurn(s string) int {
	w := 0
	for _, r := range s {
		if r > 127 {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func computeXiTStatsWithSessionForTurn(home string, windowSeconds int) (observed, autoCount, saved, sessionSaved int) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, ".xit", "history.jsonl"))
	}
	candidates = append(candidates, filepath.Join(home, "history.jsonl"))
	candidates = append(candidates, filepath.Join(home, "kimi-hooks", "events.jsonl"))

	if windowSeconds <= 0 {
		windowSeconds = 7200 // 2 hours
	}
	cutoff := time.Now().Add(-time.Duration(windowSeconds) * time.Second)

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := string(data)
		for _, line := range splitLines(lines) {
			line = trimSpace(line)
			if line == "" {
				continue
			}
			var rec struct {
				Timestamp    string `json:"timestamp"`
				RawBytes     int    `json:"raw_bytes"`
				SummaryBytes int    `json:"summary_bytes"`
				Action       string `json:"action"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			observed++
			if rec.RawBytes > 0 && rec.SummaryBytes > 0 && rec.RawBytes > rec.SummaryBytes {
				autoCount++
				saved += rec.RawBytes - rec.SummaryBytes
			}
			if rec.Action == "reroute" {
				autoCount++
			}
			ts, _ := time.Parse(time.RFC3339, rec.Timestamp)
			if !ts.IsZero() && ts.After(cutoff) && rec.RawBytes > rec.SummaryBytes {
				sessionSaved += rec.RawBytes - rec.SummaryBytes
			}
		}
	}
	return
}

// computeTurnStats computes turn-scoped stats for the given turn.
func computeTurnStats(home string, turn TurnState) TurnStats {
	autoCount, savedBytes := computeTurnStatsValues(home, turn)
	return TurnStats{
		AutoCount:   autoCount,
		SavedBytes:  savedBytes,
		SavedTokens: savedBytes / 4,
	}
}

// computeTurnStatsValues returns autoCount and savedBytes for records within the turn time range.
func computeTurnStatsValues(home string, turn TurnState) (autoCount, savedBytes int) {
	if turn.StartedAt == "" {
		return 0, 0
	}
	turnStart, err := time.Parse(time.RFC3339, turn.StartedAt)
	if err != nil {
		return 0, 0
	}
	turnEnd := time.Now()
	if turn.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339, turn.FinishedAt); err == nil {
			turnEnd = t
		}
	}

	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, ".xit", "history.jsonl"))
	}
	candidates = append(candidates, filepath.Join(home, "history.jsonl"))
	candidates = append(candidates, filepath.Join(home, "kimi-hooks", "events.jsonl"))

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := string(data)
		for _, line := range splitLines(lines) {
			line = trimSpace(line)
			if line == "" {
				continue
			}
			var rec struct {
				Timestamp    string `json:"timestamp"`
				RawBytes     int    `json:"raw_bytes"`
				SummaryBytes int    `json:"summary_bytes"`
				Action       string `json:"action"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			ts, _ := time.Parse(time.RFC3339, rec.Timestamp)
			if ts.IsZero() {
				continue
			}
			if ts.Before(turnStart) || ts.After(turnEnd) {
				continue
			}
			if rec.RawBytes > 0 && rec.SummaryBytes > 0 && rec.RawBytes > rec.SummaryBytes {
				autoCount++
				savedBytes += rec.RawBytes - rec.SummaryBytes
			}
			if rec.Action == "reroute" {
				autoCount++
			}
		}
	}
	return
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	// Simple trim for basic ASCII spaces.
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
