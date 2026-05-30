package hitrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/filters"
)

// Target thresholds for acceptance.
const (
	TargetCompressRecall       = 90.0
	TargetPassthroughPrecision = 98.0
	TargetSummaryFidelity      = 95.0
)

// Report contains the full hit-rate and fidelity audit for a CLI adapter's routing.
type Report struct {
	Adapter            string                 `json:"adapter,omitempty"`
	Window             string                 `json:"window"`
	Mode               string                 `json:"mode"`
	ShellCommandsSeen  int                    `json:"shell_commands_seen"`
	ShouldCompress     ShouldCompressStats    `json:"should_compress"`
	ShouldPassthrough  ShouldPassthroughStats `json:"should_passthrough"`
	SummaryFidelity    SummaryFidelityStats   `json:"summary_fidelity"`
	MissedHighNoise    []string               `json:"missed_high_noise"`
	MissedHighNoiseTop []CommandCount         `json:"missed_high_noise_top"`
	FalsePositive      []string               `json:"false_positive"`
	NeedsReview        []NeedsReviewItem      `json:"needs_review"`
	Recommendations    []string               `json:"recommendations"`
	MalformedEvents    int                    `json:"malformed_events"`
	Targets            TargetStats            `json:"targets"`
	Verdict            string                 `json:"verdict"`
	Freshness          FreshnessInfo          `json:"freshness"`
}

// ShouldCompressStats tracks how often high-noise commands were wrapped.
type ShouldCompressStats struct {
	Total            int     `json:"total"`
	CorrectlyWrapped int     `json:"correctly_wrapped"`
	Missed           int     `json:"missed"`
	CompressRecall   float64 `json:"compress_recall"`
}

// ShouldPassthroughStats tracks how often short/structured commands were left alone.
type ShouldPassthroughStats struct {
	Total                int     `json:"total"`
	CorrectlyPassthrough int     `json:"correctly_passthrough"`
	FalsePositive        int     `json:"false_positive"`
	PassthroughPrecision float64 `json:"passthrough_precision"`
}

// SummaryFidelityStats tracks whether xit auto summaries preserve key facts.
type SummaryFidelityStats struct {
	XitAutoRuns           int     `json:"xit_auto_runs"`
	RawLogPresent         int     `json:"raw_log_present"`
	ExitCodePresent       int     `json:"exit_code_present"`
	ReductionPresent      int     `json:"reduction_present"`
	FailureSignalPresent  int     `json:"failure_signal_present"`
	CommandSpecificSignal int     `json:"command_specific_signal"`
	PanicFree             bool    `json:"panic_free"`
	BasicFidelity         float64 `json:"basic_fidelity"`
}

// TargetStats holds the acceptance thresholds.
type TargetStats struct {
	CompressRecallTarget       string `json:"compress_recall_target"`
	PassthroughPrecisionTarget string `json:"passthrough_precision_target"`
	SummaryFidelityTarget      string `json:"summary_fidelity_target"`
}

// FreshnessInfo tracks event time range.
type FreshnessInfo struct {
	NewestEvent      string `json:"newest_event"`
	OldestEvent      string `json:"oldest_event"`
	RuleVersionKnown bool   `json:"rule_version_known"`
}

// CommandCount is a simple command → count pair.
type CommandCount struct {
	Command string `json:"command"`
	Count   int    `json:"count"`
}

// NeedsReviewItem lists commands that fall outside compress/passthrough policies.
type NeedsReviewItem struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

// adapterDisplayName returns a human-readable name for the adapter.
func adapterDisplayName(adapter string) string {
	switch adapter {
	case "claude":
		return "Claude Code"
	case "kimi":
		return "Kimi"
	default:
		if adapter == "" {
			return "Kimi"
		}
		return adapter
	}
}

// ComputeReport builds a hit-rate report for Kimi (backward-compatible wrapper).
func ComputeReport(userHome, projectHome string, window time.Duration) (*Report, error) {
	return ComputeReportForAdapter("kimi", userHome, projectHome, window)
}

// ComputeReportForAdapter builds a hit-rate report from hook events and history records
// for the specified adapter (e.g. "claude", "kimi").
func ComputeReportForAdapter(adapter, userHome, projectHome string, window time.Duration) (*Report, error) {
	display := adapterDisplayName(adapter)
	r := &Report{
		Adapter:         adapter,
		Window:          fmt.Sprintf("last %s", window),
		NeedsReview:     []NeedsReviewItem{},
		MissedHighNoise: []string{},
		FalsePositive:   []string{},
		Recommendations: []string{},
		SummaryFidelity: SummaryFidelityStats{
			PanicFree: true,
		},
		Targets: TargetStats{
			CompressRecallTarget:       fmt.Sprintf("%.0f%%", TargetCompressRecall),
			PassthroughPrecisionTarget: fmt.Sprintf("%.0f%%", TargetPassthroughPrecision),
			SummaryFidelityTarget:      fmt.Sprintf("%.0f%%", TargetSummaryFidelity),
		},
		Freshness: FreshnessInfo{},
	}

	cutoff := time.Now().Add(-window)

	events, malformed := readHookEventsForAdapter(userHome, adapter, cutoff)
	r.MalformedEvents = malformed
	historyRecs := readHistory(projectHome, userHome, cutoff)

	if len(events) > 0 {
		r.Mode = "hook_events_plus_history"
	} else {
		r.Mode = "history_only"
	}

	// Track freshness from events.
	var newestTime, oldestTime time.Time
	for _, ev := range events {
		if ev.Time != "" {
			ts, err := time.Parse(time.RFC3339, ev.Time)
			if err == nil {
				if newestTime.IsZero() || ts.After(newestTime) {
					newestTime = ts
				}
				if oldestTime.IsZero() || ts.Before(oldestTime) {
					oldestTime = ts
				}
			}
		}
	}
	if !newestTime.IsZero() {
		r.Freshness.NewestEvent = newestTime.Format(time.RFC3339)
	}
	if !oldestTime.IsZero() {
		r.Freshness.OldestEvent = oldestTime.Format(time.RFC3339)
	}
	r.Freshness.RuleVersionKnown = false

	// Routing accuracy from hook events.
	missedCounts := make(map[string]int)
	for _, ev := range events {
		orig := strings.TrimSpace(ev.OriginalCommand)
		if orig == "" {
			continue
		}
		norm, wasWrapped := normalizeCommand(orig)
		if norm == "" {
			continue
		}
		r.ShellCommandsSeen++

		policy := filters.ClassifyPolicy(strings.Fields(norm))
		switch policy {
		case "should_compress":
			r.ShouldCompress.Total++
			if wasWrapped {
				r.ShouldCompress.CorrectlyWrapped++
			} else {
				r.ShouldCompress.Missed++
				r.MissedHighNoise = append(r.MissedHighNoise, orig)
				missedCounts[norm]++
			}
		case "should_passthrough":
			r.ShouldPassthrough.Total++
			if wasWrapped {
				r.ShouldPassthrough.FalsePositive++
				r.FalsePositive = append(r.FalsePositive, orig)
			} else {
				r.ShouldPassthrough.CorrectlyPassthrough++
			}
		case "needs_review":
			r.NeedsReview = append(r.NeedsReview, NeedsReviewItem{
				Command: orig,
				Reason:  "policy: needs_review",
			})
		}
	}

	// Build missed_high_noise_top ranking.
	for cmd, count := range missedCounts {
		r.MissedHighNoiseTop = append(r.MissedHighNoiseTop, CommandCount{Command: cmd, Count: count})
	}
	sort.Slice(r.MissedHighNoiseTop, func(i, j int) bool {
		return r.MissedHighNoiseTop[i].Count > r.MissedHighNoiseTop[j].Count
	})

	// Summary fidelity from history records (Kimi only; other adapters share history and the
	// panic scan produces misleading results for unrelated raw logs).
	if adapter != "kimi" {
		historyRecs = nil
	}
	panicPatterns := []string{"panic:", "slice bounds out of range", "runtime error:", "index out of range"}
	for _, rec := range historyRecs {
		r.SummaryFidelity.XitAutoRuns++
		if rec.RawLog != "" {
			if _, err := os.Stat(rec.RawLog); err == nil {
				r.SummaryFidelity.RawLogPresent++
				data, err := os.ReadFile(rec.RawLog)
				if err == nil {
					content := string(data)
					for _, p := range panicPatterns {
						if strings.Contains(content, p) {
							r.SummaryFidelity.PanicFree = false
							break
						}
					}
				}
			}
		}
		r.SummaryFidelity.ExitCodePresent++
		if rec.EstimatedReduction > 0 || rec.SummaryBytes > 0 {
			r.SummaryFidelity.ReductionPresent++
		}

		if rec.ExitCode != 0 {
			failureKeywords := []string{"FAIL", "fail", "Error", "error", "panic", "timeout", "broken"}
			if rec.RawLog != "" {
				data, _ := os.ReadFile(rec.RawLog)
				content := string(data)
				for _, kw := range failureKeywords {
					if strings.Contains(content, kw) {
						r.SummaryFidelity.FailureSignalPresent++
						break
					}
				}
			}
		} else {
			r.SummaryFidelity.FailureSignalPresent++
		}

		norm, _ := normalizeCommand(rec.Command)
		parts := strings.Fields(norm)
		if len(parts) > 0 {
			tk := parts[0]
			if len(parts) > 1 {
				tk = parts[0] + " " + parts[1]
			}
			hasSignal := false
			switch tk {
			case "go test", "cargo test", "npm test", "pnpm test", "pytest test":
				if rec.RawLog != "" {
					data, _ := os.ReadFile(rec.RawLog)
					content := string(data)
					if strings.Contains(content, "PASS") || strings.Contains(content, "FAIL") || strings.Contains(content, "ok") {
						hasSignal = true
					}
				}
			case "git diff":
				if rec.RawLog != "" {
					data, _ := os.ReadFile(rec.RawLog)
					content := string(data)
					if strings.Contains(content, "diff --git") || strings.Contains(content, "---") || strings.Contains(content, "+++") {
						hasSignal = true
					}
				}
			case "rg", "grep":
				if rec.RawLog != "" {
					data, _ := os.ReadFile(rec.RawLog)
					content := string(data)
					if strings.Contains(content, "match") || strings.Contains(content, "Match") || strings.Count(content, "\n") > 2 {
						hasSignal = true
					}
				}
			default:
				hasSignal = rec.RawLog != ""
			}
			if hasSignal {
				r.SummaryFidelity.CommandSpecificSignal++
			}
		}
	}

	// Percentages.
	if r.ShouldCompress.Total > 0 {
		r.ShouldCompress.CompressRecall = float64(r.ShouldCompress.CorrectlyWrapped) / float64(r.ShouldCompress.Total) * 100
	}
	if r.ShouldPassthrough.Total > 0 {
		r.ShouldPassthrough.PassthroughPrecision = float64(r.ShouldPassthrough.CorrectlyPassthrough) / float64(r.ShouldPassthrough.Total) * 100
	}
	if r.SummaryFidelity.XitAutoRuns > 0 {
		passedTotal := r.SummaryFidelity.RawLogPresent + r.SummaryFidelity.ExitCodePresent + r.SummaryFidelity.ReductionPresent + r.SummaryFidelity.FailureSignalPresent + r.SummaryFidelity.CommandSpecificSignal
		r.SummaryFidelity.BasicFidelity = float64(passedTotal) / float64(5*r.SummaryFidelity.XitAutoRuns) * 100
	}

	// Verdict.
	if r.Mode == "history_only" {
		r.Verdict = "partial"
	} else if r.ShouldCompress.Total == 0 && r.ShouldPassthrough.Total == 0 {
		r.Verdict = "partial"
	} else {
		passCompress := r.ShouldCompress.Total == 0 || r.ShouldCompress.CompressRecall >= TargetCompressRecall
		passPassthrough := r.ShouldPassthrough.Total == 0 || r.ShouldPassthrough.PassthroughPrecision >= TargetPassthroughPrecision
		passFidelity := r.SummaryFidelity.XitAutoRuns == 0 || r.SummaryFidelity.BasicFidelity >= TargetSummaryFidelity
		if passCompress && passPassthrough && passFidelity {
			r.Verdict = "pass"
		} else {
			r.Verdict = "fail"
		}
	}

	// Recommendations.
	if len(r.MissedHighNoise) > 0 {
		cmdTypes := make(map[string]bool)
		for _, cmd := range r.MissedHighNoise {
			norm, _ := normalizeCommand(cmd)
			parts := strings.Fields(norm)
			if len(parts) > 0 {
				cmdTypes[parts[0]] = true
			}
		}
		for ct := range cmdTypes {
			switch ct {
			case "go":
				r.Recommendations = append(r.Recommendations, "strengthen go test verbose rule")
			case "grep", "rg":
				r.Recommendations = append(r.Recommendations, "strengthen repo search rule")
			case "git":
				r.Recommendations = append(r.Recommendations, "strengthen diff rule")
			case "docker":
				r.Recommendations = append(r.Recommendations, "strengthen docker logs rule")
			case "npm", "pnpm":
				r.Recommendations = append(r.Recommendations, "strengthen npm/pnpm test rule")
			case "cargo":
				r.Recommendations = append(r.Recommendations, "strengthen cargo test rule")
			case "pytest":
				r.Recommendations = append(r.Recommendations, "strengthen pytest rule")
			case "tsc":
				r.Recommendations = append(r.Recommendations, "strengthen tsc rule")
			case "eslint":
				r.Recommendations = append(r.Recommendations, "strengthen eslint rule")
			default:
				r.Recommendations = append(r.Recommendations, fmt.Sprintf("strengthen %s rules for %s commands", display, ct))
			}
		}
		sort.Strings(r.Recommendations)
	}
	if len(r.FalsePositive) > 0 {
		r.Recommendations = append(r.Recommendations, "strengthen passthrough rule for short/structured commands")
	}
	if r.Mode == "history_only" {
		r.Recommendations = append([]string{fmt.Sprintf("%s shell command events unavailable; install hook for miss audit", display)}, r.Recommendations...)
	}

	return r, nil
}

type hookEvent struct {
	Time               string `json:"time"`
	OriginalCommand    string `json:"original_command"`
	RecommendedCommand string `json:"recommended_command"`
	Action             string `json:"action"`
}

// readHookEventsForAdapter reads events from <home>/<adapter>-hooks/events.jsonl.
func readHookEventsForAdapter(home, adapter string, cutoff time.Time) ([]hookEvent, int) {
	var events []hookEvent
	malformed := 0
	path := filepath.Join(home, adapter+"-hooks", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return events, 0
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev hookEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			malformed++
			continue
		}
		if ev.Time != "" {
			ts, err := time.Parse(time.RFC3339, ev.Time)
			if err == nil && ts.Before(cutoff) {
				continue
			}
		}
		events = append(events, ev)
	}
	return events, malformed
}

// readHookEvents reads Kimi hook events (backward-compatible wrapper).
func readHookEvents(home string, cutoff time.Time) ([]hookEvent, int) {
	return readHookEventsForAdapter(home, "kimi", cutoff)
}

type historyRecord struct {
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

func readHistory(projectHome, userHome string, cutoff time.Time) []historyRecord {
	var recs []historyRecord
	paths := []string{
		filepath.Join(projectHome, "history.jsonl"),
		filepath.Join(userHome, "history.jsonl"),
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec historyRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.Timestamp != "" {
				ts, err := time.Parse(time.RFC3339, rec.Timestamp)
				if err == nil && ts.Before(cutoff) {
					continue
				}
			}
			if rec.Policy == "" {
				rec.Policy = "needs_review"
			}
			key := rec.Timestamp + "|" + rec.Command
			if seen[key] {
				continue
			}
			seen[key] = true
			recs = append(recs, rec)
		}
	}
	return recs
}

// normalizeCommand strips the optional "xit auto" prefix and returns the
// canonical form (basename of first token) plus whether it was wrapped.
func normalizeCommand(cmd string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	wasWrapped := false

	prefixes := []string{"./xit auto ", "xit auto "}
	for _, prefix := range prefixes {
		prefixTab := strings.TrimSuffix(prefix, " ") + "\t"
		if strings.HasPrefix(cmd, prefix) {
			cmd = strings.TrimPrefix(cmd, prefix)
			wasWrapped = true
			break
		}
		if strings.HasPrefix(cmd, prefixTab) {
			cmd = strings.TrimPrefix(cmd, prefixTab)
			wasWrapped = true
			break
		}
	}

	cmd = strings.TrimSpace(cmd)
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", false
	}
	parts[0] = filepath.Base(parts[0])
	return strings.Join(parts, " "), wasWrapped
}

// FormatReport renders a Report as human-readable text.
func FormatReport(r *Report, verbose bool) string {
	display := adapterDisplayName(r.Adapter)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("XiT %s Routing Hit Rate\n", display))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("window: %s\n", r.Window))
	b.WriteString(fmt.Sprintf("mode: %s\n", r.Mode))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("shell_commands_seen: %d\n", r.ShellCommandsSeen))
	b.WriteString("\n")
	b.WriteString("should_compress:\n")
	b.WriteString(fmt.Sprintf("  total: %d\n", r.ShouldCompress.Total))
	b.WriteString(fmt.Sprintf("  correctly_wrapped: %d\n", r.ShouldCompress.CorrectlyWrapped))
	b.WriteString(fmt.Sprintf("  missed: %d\n", r.ShouldCompress.Missed))
	b.WriteString(fmt.Sprintf("  compress_recall: %.1f%%\n", r.ShouldCompress.CompressRecall))
	b.WriteString("\n")
	b.WriteString("should_passthrough:\n")
	b.WriteString(fmt.Sprintf("  total: %d\n", r.ShouldPassthrough.Total))
	b.WriteString(fmt.Sprintf("  correctly_passthrough: %d\n", r.ShouldPassthrough.CorrectlyPassthrough))
	b.WriteString(fmt.Sprintf("  false_positive: %d\n", r.ShouldPassthrough.FalsePositive))
	b.WriteString(fmt.Sprintf("  passthrough_precision: %.1f%%\n", r.ShouldPassthrough.PassthroughPrecision))
	if r.Adapter == "" || r.Adapter == "kimi" {
		b.WriteString("\n")
		b.WriteString("summary_fidelity:\n")
		b.WriteString(fmt.Sprintf("  xit_auto_runs: %d\n", r.SummaryFidelity.XitAutoRuns))
		b.WriteString(fmt.Sprintf("  raw_log_present: %d/%d\n", r.SummaryFidelity.RawLogPresent, r.SummaryFidelity.XitAutoRuns))
		b.WriteString(fmt.Sprintf("  exit_code_present: %d/%d\n", r.SummaryFidelity.ExitCodePresent, r.SummaryFidelity.XitAutoRuns))
		b.WriteString(fmt.Sprintf("  reduction_present: %d/%d\n", r.SummaryFidelity.ReductionPresent, r.SummaryFidelity.XitAutoRuns))
		b.WriteString(fmt.Sprintf("  failure_signal_present: %d/%d\n", r.SummaryFidelity.FailureSignalPresent, r.SummaryFidelity.XitAutoRuns))
		b.WriteString(fmt.Sprintf("  command_specific_signal: %d/%d\n", r.SummaryFidelity.CommandSpecificSignal, r.SummaryFidelity.XitAutoRuns))
		panicStr := "yes"
		if !r.SummaryFidelity.PanicFree {
			panicStr = "no"
		}
		b.WriteString(fmt.Sprintf("  panic_free: %s\n", panicStr))
		b.WriteString(fmt.Sprintf("  basic_fidelity: %.1f%%\n", r.SummaryFidelity.BasicFidelity))
		b.WriteString("\n")
	}

	overallTotal := r.ShouldCompress.Total + r.ShouldPassthrough.Total
	overallCorrect := r.ShouldCompress.CorrectlyWrapped + r.ShouldPassthrough.CorrectlyPassthrough
	if overallTotal > 0 {
		b.WriteString("overall:\n")
		b.WriteString(fmt.Sprintf("  routing_hit_rate: %.1f%%\n", float64(overallCorrect)/float64(overallTotal)*100))
		b.WriteString("\n")
	}

	b.WriteString("targets:\n")
	b.WriteString(fmt.Sprintf("  compress_recall_target: %s\n", r.Targets.CompressRecallTarget))
	b.WriteString(fmt.Sprintf("  passthrough_precision_target: %s\n", r.Targets.PassthroughPrecisionTarget))
	b.WriteString(fmt.Sprintf("  summary_fidelity_target: %s\n", r.Targets.SummaryFidelityTarget))
	b.WriteString(fmt.Sprintf("verdict: %s\n", r.Verdict))
	b.WriteString("\n")

	b.WriteString("missed_high_noise:\n")
	if len(r.MissedHighNoise) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, cmd := range r.MissedHighNoise {
			b.WriteString(fmt.Sprintf("- %s\n", cmd))
		}
	}
	if len(r.MissedHighNoiseTop) > 0 {
		b.WriteString("\n")
		b.WriteString("missed_high_noise_top:\n")
		for _, mc := range r.MissedHighNoiseTop {
			b.WriteString(fmt.Sprintf("- %s: %d\n", mc.Command, mc.Count))
		}
	}
	b.WriteString("\n")
	b.WriteString("false_positive:\n")
	if len(r.FalsePositive) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, cmd := range r.FalsePositive {
			b.WriteString(fmt.Sprintf("- %s\n", cmd))
		}
	}
	if len(r.NeedsReview) > 0 {
		b.WriteString("\n")
		b.WriteString("needs_review:\n")
		for _, nr := range r.NeedsReview {
			b.WriteString(fmt.Sprintf("- %s (%s)\n", nr.Command, nr.Reason))
		}
	}
	b.WriteString("\n")
	b.WriteString("recommendation:\n")
	if len(r.Recommendations) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, rec := range r.Recommendations {
			b.WriteString(fmt.Sprintf("- %s\n", rec))
		}
	}
	if r.Mode == "history_only" {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("warning: %s shell command events unavailable; miss audit limited.\n", display))
	}
	if r.Freshness.OldestEvent != "" {
		b.WriteString("\n")
		b.WriteString("freshness:\n")
		b.WriteString(fmt.Sprintf("  newest_event: %s\n", r.Freshness.NewestEvent))
		b.WriteString(fmt.Sprintf("  oldest_event: %s\n", r.Freshness.OldestEvent))
		b.WriteString(fmt.Sprintf("  rule_version_known: %v\n", r.Freshness.RuleVersionKnown))
	}
	return b.String()
}
