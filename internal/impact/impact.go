package impact

import (
	"fmt"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/history"
)

// Report contains the XiT context impact analysis.
type Report struct {
	Window               string        `json:"window"`
	Mode                 string        `json:"mode"`
	KimiContextTokens    int           `json:"kimi_context_tokens"`
	KimiContextAvailable bool          `json:"kimi_context_available"`
	XitSession           XitSession    `json:"xit_session"`
	Impact               ImpactStats   `json:"impact"`
	Interpretation       string        `json:"interpretation"`
	Recommendations      []string      `json:"recommendations"`
	ContextNote          string        `json:"context_note"`
}

// XitSession holds the XiT savings within the time window.
type XitSession struct {
	AutoCommands         int    `json:"auto_commands"`
	SavedBytes           int    `json:"saved_bytes"`
	EstimatedSavedTokens int    `json:"estimated_saved_tokens"`
	EstimateMethod       string `json:"estimate_method"`
}

// ImpactStats holds the computed impact metrics.
type ImpactStats struct {
	SavedVsContext float64 `json:"saved_vs_context"`
	Verdict        string  `json:"verdict"`
}

// ComputeReport builds a context impact report.
// kimiContextTokens may be 0 if the user did not provide it.
func ComputeReport(baseDir string, window time.Duration, kimiContextTokens int) (*Report, error) {
	m, err := history.ComputeSessionMetrics(baseDir, window)
	if err != nil {
		return nil, err
	}

	savedBytes := m.CurrentSession.SavedBytes
	estimatedTokens := savedBytes / 4

	r := &Report{
		Window:               fmt.Sprintf("last %s", window),
		Mode:                 m.Mode,
		KimiContextTokens:    kimiContextTokens,
		KimiContextAvailable: kimiContextTokens > 0,
		XitSession: XitSession{
			AutoCommands:         m.CurrentSession.AutoCommands,
			SavedBytes:           savedBytes,
			EstimatedSavedTokens: estimatedTokens,
			EstimateMethod:       "saved_bytes / 4",
		},
		Recommendations: []string{},
		ContextNote:     "Kimi context includes user prompts, assistant reasoning, reports, and tool summaries. XiT only reduces command output that passes through xit auto.",
	}

	if kimiContextTokens > 0 && savedBytes > 0 {
		r.Impact.SavedVsContext = float64(estimatedTokens) / float64(kimiContextTokens) * 100
	} else if savedBytes <= 0 {
		r.Impact.SavedVsContext = 0
	}

	switch {
	case r.Impact.SavedVsContext >= 30:
		r.Impact.Verdict = "strong"
		r.Interpretation = "XiT savings offset a significant portion of Kimi context."
	case r.Impact.SavedVsContext >= 15:
		r.Impact.Verdict = "moderate"
		r.Interpretation = "XiT provides meaningful context relief, but more routing hits would help."
	case r.Impact.SavedVsContext >= 5:
		r.Impact.Verdict = "weak"
		r.Interpretation = "XiT compressed command output successfully, but current Kimi session spent most tokens outside xit auto."
	default:
		r.Impact.Verdict = "very_weak"
		r.Interpretation = "XiT savings are a tiny fraction of Kimi context. Routing hit rate and report verbosity need improvement."
	}

	if !r.KimiContextAvailable {
		r.Interpretation = "XiT session savings are tracked, but Kimi context size was not provided. Pass --kimi-context to compute impact."
	}

	if r.Impact.Verdict == "weak" || r.Impact.Verdict == "very_weak" {
		r.Recommendations = append(r.Recommendations, "improve routing hit rate")
		r.Recommendations = append(r.Recommendations, "reduce final report verbosity")
		r.Recommendations = append(r.Recommendations, "require xit auto for high-noise shell commands")
	}

	return r, nil
}

// FormatReport renders a Report as human-readable text.
func FormatReport(r *Report) string {
	var b strings.Builder
	b.WriteString("XiT Kimi Context Impact\n")
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("window: %s\n", r.Window))
	b.WriteString(fmt.Sprintf("mode: %s\n", r.Mode))
	if r.KimiContextAvailable {
		b.WriteString(fmt.Sprintf("kimi_context_tokens: %d\n", r.KimiContextTokens))
	} else {
		b.WriteString("kimi_context_tokens: unavailable\n")
		b.WriteString("hint: pass --kimi-context 149k from Kimi bottom toolbar.\n")
	}
	b.WriteString("\n")
	b.WriteString("xit_session:\n")
	b.WriteString(fmt.Sprintf("  auto_commands: %d\n", r.XitSession.AutoCommands))
	b.WriteString(fmt.Sprintf("  saved_bytes: %d\n", r.XitSession.SavedBytes))
	b.WriteString(fmt.Sprintf("  saved_tokens: %d\n", r.XitSession.EstimatedSavedTokens))
	b.WriteString(fmt.Sprintf("  token_method: %s\n", r.XitSession.EstimateMethod))
	b.WriteString("\n")
	b.WriteString("impact:\n")
	if r.KimiContextAvailable {
		b.WriteString(fmt.Sprintf("  saved_vs_context: %.1f%%\n", r.Impact.SavedVsContext))
		b.WriteString(fmt.Sprintf("  verdict: %s\n", r.Impact.Verdict))
	} else {
		b.WriteString("  saved_vs_context: unavailable\n")
		b.WriteString(fmt.Sprintf("  verdict: %s\n", r.Impact.Verdict))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("interpretation:\n  %s\n", r.Interpretation))
	if len(r.Recommendations) > 0 {
		b.WriteString("\n")
		b.WriteString("recommendations:\n")
		for _, rec := range r.Recommendations {
			b.WriteString(fmt.Sprintf("- %s\n", rec))
		}
	}
	b.WriteString("\n")
	b.WriteString("context_note:\n")
	b.WriteString("  Kimi context includes user prompts, assistant reasoning, reports, and tool summaries.\n")
	b.WriteString("  XiT only reduces command output that passes through xit auto.\n")
	b.WriteString("\n")
	b.WriteString("  Kimi context 包含用户输入、模型思考、最终报告和工具摘要；XiT 当前只压缩经过 xit auto 的命令输出。\n")
	return b.String()
}

// ParseContextTokens parses user-provided context strings like "149k" or "149000".
func ParseContextTokens(s string) int {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	// Handle suffixes like k, m
	multiplier := 1
	if strings.HasSuffix(s, "k") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "k")
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1000000
		s = strings.TrimSuffix(s, "m")
	}
	var val int
	fmt.Sscanf(s, "%d", &val)
	return val * multiplier
}
