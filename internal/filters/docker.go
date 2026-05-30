package filters

import (
	"fmt"
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
	"github.com/stephenywilson/xit/internal/util"
)

func filterDocker(args []string, res *runner.Result) (*output.Summary, error) {
	if len(args) < 2 {
		return fallback(args, res), nil
	}
	sub := args[1]
	switch sub {
	case "ps":
		return filterDockerPs(args, res)
	case "logs":
		return filterDockerLogs(args, res)
	}
	return fallback(args, res), nil
}

func filterDockerPs(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	var containers []string
	for _, line := range lines {
		if strings.Contains(line, "CONTAINER") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			containers = append(containers, fmt.Sprintf("%s (%s) %s", fields[0], fields[1], fields[len(fields)-1]))
		}
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(containers)),
		KeyFacts: map[string]interface{}{
			"containers": len(containers),
		},
		BodyLines: containers,
	}
	return s, nil
}

func filterDockerLogs(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	lines = util.RunDedupWithCount(lines)
	lines = safeTruncateLines(lines, 50, 50)

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(lines)),
		KeyFacts: map[string]interface{}{
			"unique_lines": len(lines),
		},
		BodyLines: lines,
	}
	return s, nil
}
