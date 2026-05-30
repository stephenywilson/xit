package filters

import (
	"fmt"
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
)

func filterSearch(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	matches := make(map[string][]string)
	pattern := extractPattern(args)

	for _, line := range lines {
		if line == "" {
			continue
		}
		// ripgrep/grep default format: path:line:text or path:text
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 2 {
			path := parts[0]
			rest := strings.Join(parts[1:], ":")
			matches[path] = append(matches[path], rest)
		} else {
			matches["unknown"] = append(matches["unknown"], line)
		}
	}

	var body []string
	truncated := false
	totalMatches := 0
	for path, ms := range matches {
		totalMatches += len(ms)
		show := ms
		if len(show) > 3 {
			show = show[:3]
			truncated = true
		}
		body = append(body, fmt.Sprintf("%s:", path))
		for _, m := range show {
			body = append(body, "  "+m)
		}
	}

	if truncated {
		body = append(body, "... [truncated, max 3 per file] ...")
	}
	if totalMatches > 100 {
		body = append(body, fmt.Sprintf("... total matches: %d ...", totalMatches))
	}

	confidence := "high"
	if pattern == "" || pattern == "unknown" {
		confidence = "medium"
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         confidence,
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(body)),
		KeyFacts: map[string]interface{}{
			"pattern":       pattern,
			"files_matched": len(matches),
			"total_matches": totalMatches,
		},
		BodyLines: body,
	}
	return s, nil
}

func extractPattern(args []string) string {
	if len(args) < 2 {
		return "unknown"
	}
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return "unknown"
}
