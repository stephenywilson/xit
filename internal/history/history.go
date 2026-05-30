package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Record struct {
	Timestamp          string  `json:"timestamp"`
	Command            string  `json:"command"`
	ExitCode           int     `json:"exit_code"`
	RawBytes           int     `json:"raw_bytes"`
	SummaryBytes       int     `json:"summary_bytes"`
	EstimatedReduction float64 `json:"estimated_reduction"`
	DurationMs         int64   `json:"duration_ms"`
	Filter             string  `json:"filter"`
	Confidence         string  `json:"confidence"`
	Policy             string  `json:"policy"`
	RawLog             string  `json:"raw_log"`
}

func Append(baseDir string, r Record) error {
	path := filepath.Join(baseDir, "history.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, string(data))
	return err
}

type Gain struct {
	TotalCommands      int
	TotalRawBytes      int
	TotalSummaryBytes  int
	EstimatedSavedBytes int
	EstimatedReduction float64
	TopCommands        []CommandSavings
}

type CommandSavings struct {
	Command string
	Count   int
	Saved   int
}

func ComputeGain(baseDir string) (*Gain, error) {
	path := filepath.Join(baseDir, "history.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Gain{}, nil
		}
		return nil, err
	}

	g := &Gain{}
	cmdSavings := make(map[string]*CommandSavings)

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		g.TotalCommands++
		g.TotalRawBytes += r.RawBytes
		g.TotalSummaryBytes += r.SummaryBytes
		saved := r.RawBytes - r.SummaryBytes
		if saved < 0 {
			saved = 0
		}
		g.EstimatedSavedBytes += saved

		cs, ok := cmdSavings[r.Command]
		if !ok {
			cs = &CommandSavings{Command: r.Command}
			cmdSavings[r.Command] = cs
		}
		cs.Count++
		cs.Saved += saved
	}

	if g.TotalRawBytes > 0 {
		g.EstimatedReduction = float64(g.EstimatedSavedBytes) / float64(g.TotalRawBytes)
	}

	for _, cs := range cmdSavings {
		g.TopCommands = append(g.TopCommands, *cs)
	}
	sort.Slice(g.TopCommands, func(i, j int) bool {
		return g.TopCommands[i].Saved > g.TopCommands[j].Saved
	})
	if len(g.TopCommands) > 5 {
		g.TopCommands = g.TopCommands[:5]
	}

	return g, nil
}

type SessionMetrics struct {
	Mode               string
	Window             time.Duration
	SessionID          string
	CurrentSession     SessionStats
	Lifetime           SessionStats
}

type SessionStats struct {
	AutoCommands   int
	RawBytes       int
	SummaryBytes   int
	SavedBytes     int
	Reduction      float64
	RawLogs        []string
}

func ComputeSessionMetrics(baseDir string, window time.Duration) (*SessionMetrics, error) {
	path := filepath.Join(baseDir, "history.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SessionMetrics{
				Mode:      "inferred_time_window",
				Window:    window,
				SessionID: "unavailable",
			}, nil
		}
		return nil, err
	}

	if window <= 0 {
		window = 2 * time.Hour
	}
	cutoff := time.Now().Add(-window)

	m := &SessionMetrics{
		Mode:      "inferred_time_window",
		Window:    window,
		SessionID: "unavailable",
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, r.Timestamp)
		if err != nil {
			continue
		}

		saved := r.RawBytes - r.SummaryBytes
		if saved < 0 {
			saved = 0
		}

		m.Lifetime.AutoCommands++
		m.Lifetime.RawBytes += r.RawBytes
		m.Lifetime.SummaryBytes += r.SummaryBytes
		m.Lifetime.SavedBytes += saved
		if r.RawLog != "" {
			m.Lifetime.RawLogs = append(m.Lifetime.RawLogs, r.RawLog)
		}

		if ts.After(cutoff) {
			m.CurrentSession.AutoCommands++
			m.CurrentSession.RawBytes += r.RawBytes
			m.CurrentSession.SummaryBytes += r.SummaryBytes
			m.CurrentSession.SavedBytes += saved
			if r.RawLog != "" {
				m.CurrentSession.RawLogs = append(m.CurrentSession.RawLogs, r.RawLog)
			}
		}
	}

	if m.Lifetime.RawBytes > 0 {
		m.Lifetime.Reduction = float64(m.Lifetime.SavedBytes) / float64(m.Lifetime.RawBytes)
	}
	if m.CurrentSession.RawBytes > 0 {
		m.CurrentSession.Reduction = float64(m.CurrentSession.SavedBytes) / float64(m.CurrentSession.RawBytes)
	}

	return m, nil
}

func FormatSessionMetrics(m *SessionMetrics, useTokens bool) string {
	var b strings.Builder
	b.WriteString("XiT Kimi Session\n")
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("mode:        %s\n", m.Mode))
	b.WriteString(fmt.Sprintf("window:      last %s\n", m.Window))
	b.WriteString(fmt.Sprintf("session_id:  %s\n", m.SessionID))
	b.WriteString("\n")
	b.WriteString("current_session:\n")
	b.WriteString(fmt.Sprintf("  auto_commands:  %d\n", m.CurrentSession.AutoCommands))
	b.WriteString(fmt.Sprintf("  raw_bytes:      %d\n", m.CurrentSession.RawBytes))
	b.WriteString(fmt.Sprintf("  summary_bytes:  %d\n", m.CurrentSession.SummaryBytes))
	b.WriteString(fmt.Sprintf("  saved_bytes:    %d\n", m.CurrentSession.SavedBytes))
	b.WriteString(fmt.Sprintf("  reduction:      %.1f%%\n", m.CurrentSession.Reduction*100))
	if useTokens {
		b.WriteString(fmt.Sprintf("  saved_tokens:   %d\n", m.CurrentSession.SavedBytes/4))
		b.WriteString("  token_method:   saved_bytes / 4\n")
	}
	b.WriteString(fmt.Sprintf("  raw_logs:       %d\n", len(m.CurrentSession.RawLogs)))
	b.WriteString("\n")
	b.WriteString("lifetime:\n")
	b.WriteString(fmt.Sprintf("  auto_commands:  %d\n", m.Lifetime.AutoCommands))
	b.WriteString(fmt.Sprintf("  saved_bytes:    %d\n", m.Lifetime.SavedBytes))
	b.WriteString(fmt.Sprintf("  reduction:      %.1f%%\n", m.Lifetime.Reduction*100))
	if useTokens {
		b.WriteString(fmt.Sprintf("  saved_tokens:   %d\n", m.Lifetime.SavedBytes/4))
		b.WriteString("  token_method:   saved_bytes / 4\n")
	}
	return b.String()
}

func FormatGain(g *Gain) string {
	var b strings.Builder
	b.WriteString("XiT Gain Report\n")
	b.WriteString(fmt.Sprintf("Total commands condensed: %d\n", g.TotalCommands))
	b.WriteString(fmt.Sprintf("Total raw bytes: %d\n", g.TotalRawBytes))
	b.WriteString(fmt.Sprintf("Total summary bytes: %d\n", g.TotalSummaryBytes))
	b.WriteString(fmt.Sprintf("Estimated saved bytes: %d\n", g.EstimatedSavedBytes))
	b.WriteString(fmt.Sprintf("Estimated reduction: %.1f%%\n", g.EstimatedReduction*100))
	if len(g.TopCommands) > 0 {
		b.WriteString("\nTop commands by savings:\n")
		for _, c := range g.TopCommands {
			b.WriteString(fmt.Sprintf("* %s: %d runs, %d bytes saved\n", c.Command, c.Count, c.Saved))
		}
	}
	return b.String()
}

type BenchGroup struct {
	Count          int
	RawBytes       int
	SummaryBytes   int
	AvgReduction   float64
	DominantConfidence string
}

type BenchReport struct {
	TotalCommands     int
	TotalRawBytes     int
	TotalSummaryBytes int
	OverallReduction  float64
	ByFilter          map[string]*BenchGroup
	ByConfidence      map[string]*BenchGroup
	ByPolicy          map[string]*BenchGroup
}

func ComputeBenchReport(baseDir string) (*BenchReport, error) {
	path := filepath.Join(baseDir, "history.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &BenchReport{
				ByFilter:     make(map[string]*BenchGroup),
				ByConfidence: make(map[string]*BenchGroup),
				ByPolicy:     make(map[string]*BenchGroup),
			}, nil
		}
		return nil, err
	}

	br := &BenchReport{
		ByFilter:     make(map[string]*BenchGroup),
		ByConfidence: make(map[string]*BenchGroup),
		ByPolicy:     make(map[string]*BenchGroup),
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		br.TotalCommands++
		br.TotalRawBytes += r.RawBytes
		br.TotalSummaryBytes += r.SummaryBytes

		addToGroup(br.ByFilter, r.Filter, r)
		addToGroup(br.ByConfidence, r.Confidence, r)
		policy := r.Policy
		if policy == "" {
			policy = "needs_review"
		}
		addToGroup(br.ByPolicy, policy, r)
	}

	if br.TotalRawBytes > 0 {
		br.OverallReduction = float64(br.TotalRawBytes-br.TotalSummaryBytes) / float64(br.TotalRawBytes)
	}

	finalizeGroups(br.ByFilter)
	finalizeGroups(br.ByConfidence)
	finalizeGroups(br.ByPolicy)

	return br, nil
}

func addToGroup(m map[string]*BenchGroup, key string, r Record) {
	g, ok := m[key]
	if !ok {
		g = &BenchGroup{}
		m[key] = g
	}
	g.Count++
	g.RawBytes += r.RawBytes
	g.SummaryBytes += r.SummaryBytes
}

func finalizeGroups(m map[string]*BenchGroup) {
	for _, g := range m {
		if g.RawBytes > 0 {
			g.AvgReduction = float64(g.RawBytes-g.SummaryBytes) / float64(g.RawBytes)
		}
	}
}
