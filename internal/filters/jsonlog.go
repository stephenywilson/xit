package filters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
	"github.com/stephenywilson/xit/internal/util"
)

func filterJSONLog(args []string, res *runner.Result) (*output.Summary, error) {
	// Try JSON first
	var raw interface{}
	if err := json.Unmarshal(res.Stdout, &raw); err == nil {
		return filterJSON(args, res, raw)
	}
	return filterLog(args, res)
}

func filterJSON(args []string, res *runner.Result, data interface{}) (*output.Summary, error) {
	var body []string
	switch v := data.(type) {
	case map[string]interface{}:
		body = append(body, "JSON object keys:")
		for k, val := range v {
			summary := summarizeValue(val)
			body = append(body, fmt.Sprintf("  %s: %s", k, summary))
		}
	case []interface{}:
		body = append(body, fmt.Sprintf("JSON array: %d items", len(v)))
		for i, item := range v {
			if i >= 10 {
				body = append(body, "  ...")
				break
			}
			body = append(body, fmt.Sprintf("  [%d]: %s", i, summarizeValue(item)))
		}
	default:
		body = append(body, fmt.Sprintf("JSON scalar: %v", v))
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(body)),
		KeyFacts: map[string]interface{}{
			"type": "json",
		},
		BodyLines: body,
	}
	return s, nil
}

func summarizeValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		if len(val) > 80 {
			return fmt.Sprintf("\"%s...\" [len=%d]", val[:80], len(val))
		}
		return fmt.Sprintf("%q", val)
	case float64:
		return fmt.Sprintf("%v", val)
	case bool:
		return fmt.Sprintf("%v", val)
	case nil:
		return "null"
	case map[string]interface{}:
		return fmt.Sprintf("{...} [%d keys]", len(val))
	case []interface{}:
		return fmt.Sprintf("[...] [%d items]", len(val))
	default:
		return fmt.Sprintf("%T", val)
	}
}

func filterLog(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	var errors, warns, infos []string
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") || strings.Contains(lower, "panic") {
			errors = append(errors, line)
		} else if strings.Contains(lower, "warn") {
			warns = append(warns, line)
		} else {
			infos = append(infos, line)
		}
	}

	lines = util.RunDedupWithCount(lines)
	var body []string
	if len(errors) > 0 {
		body = append(body, "Errors:")
		for _, e := range util.TruncateLines(errors, 20) {
			body = append(body, "  "+e)
		}
	}
	if len(warns) > 0 {
		body = append(body, "Warnings:")
		for _, w := range util.TruncateLines(warns, 20) {
			body = append(body, "  "+w)
		}
	}
	if len(infos) > 0 && len(body) < 50 {
		remaining := 50 - len(body)
		body = append(body, "Info:")
		for _, i := range util.TruncateLines(infos, remaining) {
			body = append(body, "  "+i)
		}
	}
	if len(lines) > 100 {
		body = append(body, fmt.Sprintf("... total unique lines: %d ...", len(lines)))
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "medium",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(body)),
		KeyFacts: map[string]interface{}{
			"errors":   len(errors),
			"warnings": len(warns),
		},
		BodyLines: body,
	}
	return s, nil
}
