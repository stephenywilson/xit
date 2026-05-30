package filters

import (
	"fmt"
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
)

func filterLint(args []string, res *runner.Result) (*output.Summary, error) {
	stdout := string(res.Stdout)
	stderr := string(res.Stderr)
	combined := stdout + "\n" + stderr

	lines := strings.Split(combined, "\n")
	fileErrors := make(map[string][]string)
	codeCounts := make(map[string]int)
	total := 0

	for _, line := range lines {
		if line == "" {
			continue
		}
		// Try to parse typical lint error line: path(line,col): error TS1234: msg
		if idx := strings.Index(line, ":"); idx > 0 {
			path := line[:idx]
			rest := line[idx+1:]
			fileErrors[path] = append(fileErrors[path], rest)
			// extract code like TS1234 or ESLint rule
			code := ""
			if start := strings.Index(rest, "TS"); start >= 0 {
				end := start + 2
				for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
					end++
				}
				if end > start+2 {
					code = rest[start:end]
				}
			}
			if code == "" && strings.Contains(rest, "error") {
				parts := strings.Fields(rest)
				for _, p := range parts {
					if strings.Contains(p, "/") || strings.Contains(p, "-") {
						code = p
						break
					}
				}
			}
			if code != "" {
				codeCounts[code]++
			}
			total++
		}
	}

	var body []string
	shown := 0
	for path, errs := range fileErrors {
		body = append(body, fmt.Sprintf("%s: %d errors", path, len(errs)))
		for _, e := range errs {
			if shown >= 30 {
				break
			}
			body = append(body, "  "+e)
			shown++
		}
		if shown >= 30 {
			break
		}
	}
	if total > 30 {
		body = append(body, fmt.Sprintf("... and %d more errors ...", total-30))
	}

	keyFacts := map[string]interface{}{
		"files_with_errors": len(fileErrors),
		"total_errors":      total,
	}
	if len(codeCounts) > 0 {
		var topCodes []string
		for c, n := range codeCounts {
			topCodes = append(topCodes, fmt.Sprintf("%s:%d", c, n))
		}
		keyFacts["top_codes"] = strings.Join(topCodes, ", ")
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(body)),
		KeyFacts:           keyFacts,
		BodyLines:          body,
	}
	return s, nil
}
