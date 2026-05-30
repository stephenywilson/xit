package filters

import (
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
)

func filterTest(args []string, res *runner.Result) (*output.Summary, error) {
	stdout := string(res.Stdout)
	stderr := string(res.Stderr)
	combined := stdout + "\n" + stderr

	isTest := false
	for _, a := range args {
		if a == "test" {
			isTest = true
			break
		}
	}
	if !isTest {
		// npm run build etc, fallback
		return fallback(args, res), nil
	}

	lines := strings.Split(combined, "\n")
	var failed []string
	var errors []string
	var summary string
	var stackTops []string
	inStack := false

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.Contains(line, "PASS") || strings.Contains(line, "FAIL") || strings.Contains(line, "Tests:") || strings.Contains(line, "ok ") {
			summary = trim
		}
		if strings.HasPrefix(trim, "FAIL") || strings.Contains(line, " failed") || strings.Contains(line, "Error:") || strings.Contains(line, "error ") {
			failed = append(failed, trim)
		}
		if strings.Contains(line, " at ") || strings.Contains(line, ".go:") || strings.Contains(line, ".ts:") || strings.Contains(line, ".js:") {
			if !inStack {
				stackTops = append(stackTops, trim)
				inStack = true
			}
		} else {
			inStack = false
		}
		if strings.Contains(line, "Error:") || strings.Contains(line, "error:") {
			errors = append(errors, trim)
		}
	}

	keyFacts := map[string]interface{}{
		"exit_code": res.ExitCode,
	}
	if summary != "" {
		keyFacts["summary"] = summary
	}

	var suggestions []string
	if res.ExitCode != 0 {
		suggestions = []string{
			"Focus on failed tests first.",
			"Check stack traces for root cause.",
			"Verify recent changes to error-related files.",
		}
	} else {
		suggestions = []string{
			"All tests passed.",
			"Consider running integration tests next.",
		}
	}

	var body []string
	if res.ExitCode != 0 {
		if len(failed) > 0 {
			body = append(body, "Failed tests:")
			for _, f := range failed {
				body = append(body, "  "+f)
			}
		}
		if len(errors) > 0 {
			body = append(body, "Errors:")
			for _, e := range errors {
				body = append(body, "  "+e)
			}
		}
		if len(stackTops) > 0 {
			body = append(body, "Stack tops:")
			for _, st := range stackTops {
				body = append(body, "  "+st)
			}
		}
	} else {
		body = append(body, summary)
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(body)),
		KeyFacts:           keyFacts,
		Suggestions:        suggestions,
		BodyLines:          body,
	}
	return s, nil
}
