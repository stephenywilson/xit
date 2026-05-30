package output

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Summary struct {
	Command            string
	ExitCode           int
	DurationMs         int64
	RawLogPath         string
	Confidence         string
	EstimatedReduction float64
	Filter             string
	Policy             string

	KeyFacts    map[string]interface{}
	FileList    []string
	Suggestions []string
	BodyLines   []string
}

func (s *Summary) Render(mode string) string {
	switch mode {
	case "agent":
		return s.renderAgent()
	case "json":
		return s.renderJSON()
	default:
		return s.renderHuman()
	}
}

func (s *Summary) renderHuman() string {
	var b strings.Builder

	b.WriteString("XiT Summary\n")
	b.WriteString(fmt.Sprintf("command: %s\n", s.Command))
	b.WriteString(fmt.Sprintf("exit_code: %d\n", s.ExitCode))
	b.WriteString(fmt.Sprintf("duration_ms: %d\n", s.DurationMs))
	b.WriteString(fmt.Sprintf("raw_log: %s\n", s.RawLogPath))
	b.WriteString(fmt.Sprintf("summary_confidence: %s\n", s.Confidence))
	b.WriteString(fmt.Sprintf("estimated_reduction: %.0f%%\n", s.EstimatedReduction*100))

	b.WriteString("\nKey facts:\n")
	for k, v := range s.KeyFacts {
		b.WriteString(fmt.Sprintf("* %s: %v\n", k, v))
	}

	if len(s.FileList) > 0 {
		b.WriteString("\nImportant files:\n")
		for _, f := range s.FileList {
			b.WriteString(fmt.Sprintf("* %s\n", f))
		}
	}

	if len(s.Suggestions) > 0 {
		b.WriteString("\nSuggested AI focus:\n")
		for i, sug := range s.Suggestions {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, sug))
		}
	}

	if len(s.BodyLines) > 0 {
		b.WriteString("\n")
		for _, line := range s.BodyLines {
			b.WriteString(line + "\n")
		}
	}

	return b.String()
}

func (s *Summary) renderAgent() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("command: %s | exit_code: %d | duration_ms: %d | confidence: %s | reduction: %.0f%%\n",
		s.Command, s.ExitCode, s.DurationMs, s.Confidence, s.EstimatedReduction*100))
	b.WriteString(fmt.Sprintf("raw_log: %s\n", s.RawLogPath))

	if len(s.KeyFacts) > 0 {
		b.WriteString("Key facts: ")
		parts := make([]string, 0, len(s.KeyFacts))
		for k, v := range s.KeyFacts {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}

	if len(s.FileList) > 0 {
		b.WriteString(fmt.Sprintf("Files: %s\n", strings.Join(s.FileList, ", ")))
	}

	if len(s.Suggestions) > 0 {
		b.WriteString(fmt.Sprintf("Focus: %s\n", strings.Join(s.Suggestions, "; ")))
	}

	if len(s.BodyLines) > 0 {
		b.WriteString("---\n")
		for _, line := range s.BodyLines {
			b.WriteString(line + "\n")
		}
	}

	return b.String()
}

func (s *Summary) renderJSON() string {
	data := map[string]interface{}{
		"command":             s.Command,
		"exit_code":           s.ExitCode,
		"duration_ms":         s.DurationMs,
		"raw_log":             s.RawLogPath,
		"filter":              s.Filter,
		"confidence":          s.Confidence,
		"estimated_reduction": s.EstimatedReduction,
		"key_facts":           s.KeyFacts,
		"important_files":     s.FileList,
		"suggested_ai_focus":  s.Suggestions,
		"summary":             strings.Join(s.BodyLines, "\n"),
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err)
	}
	return string(b) + "\n"
}
