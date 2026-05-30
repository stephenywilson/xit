package filters

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
	"github.com/stephenywilson/xit/internal/util"
)

func filterGit(args []string, res *runner.Result) (*output.Summary, error) {
	if len(args) < 2 {
		return fallback(args, res), nil
	}
	sub := args[1]
	switch sub {
	case "status":
		return filterGitStatus(args, res)
	case "diff":
		return filterGitDiff(args, res)
	case "log":
		return filterGitLog(args, res)
	}
	return fallback(args, res), nil
}

func filterGitStatus(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	branch := ""
	var staged, unstaged, untracked []string
	section := ""

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			branch = strings.TrimPrefix(line, "## ")
			if idx := strings.Index(branch, "..."); idx >= 0 {
				branch = branch[:idx]
			}
			continue
		}
		if strings.HasPrefix(line, "Changes to be committed") {
			section = "staged"
			continue
		}
		if strings.HasPrefix(line, "Changes not staged") {
			section = "unstaged"
			continue
		}
		if strings.HasPrefix(line, "Untracked files") {
			section = "untracked"
			continue
		}
		if len(line) < 2 {
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			text := strings.TrimSpace(line)
			switch section {
			case "staged":
				staged = append(staged, text)
			case "unstaged":
				unstaged = append(unstaged, text)
			case "untracked":
				untracked = append(untracked, text)
			}
		}
		if line[0] == 'A' || line[0] == 'M' || line[0] == 'D' || line[0] == 'R' || line[0] == 'C' {
			if section == "staged" || section == "" {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					staged = append(staged, fields[1])
				}
			}
		}
		if len(line) >= 2 && (line[1] == 'M' || line[1] == 'D') {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				unstaged = append(unstaged, fields[1])
			}
		}
		if strings.HasPrefix(line, "??") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				untracked = append(untracked, fields[1])
			}
		}
	}

	total := len(staged) + len(unstaged) + len(untracked)
	keyFacts := map[string]interface{}{
		"branch":              branch,
		"staged":              len(staged),
		"unstaged":            len(unstaged),
		"untracked":           len(untracked),
	}

	var important []string
	allFiles := append(append(staged, unstaged...), untracked...)
	if len(allFiles) > 20 {
		groups := util.GroupByDir(allFiles)
		for dir, files := range groups {
			important = append(important, fmt.Sprintf("%s/ (%d files)", dir, len(files)))
		}
		if len(important) > 20 {
			important = important[:20]
		}
	} else {
		important = allFiles
	}

	suggestions := []string{
		"Review staged changes before committing.",
		"Check for secrets or debug code in untracked files.",
	}
	if len(unstaged) > 10 {
		suggestions = append(suggestions, "Consider breaking large unstaged changes into smaller commits.")
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), total),
		KeyFacts:           keyFacts,
		FileList:           important,
		Suggestions:        suggestions,
	}
	return s, nil
}

func filterGitDiff(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	var files []string
	var hunks []string
	adds, dels := 0, 0
	var highRisk []string

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				f := strings.TrimPrefix(parts[2], "a/")
				files = append(files, f)
				if util.IsHighRiskPath(f) {
					highRisk = append(highRisk, f)
				}
			}
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			if len(hunks) < len(files)*3 {
				hunks = append(hunks, fmt.Sprintf("%s: %s", filepath.Base(files[len(files)-1]), line))
			}
			continue
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			adds++
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			dels++
		}
	}

	keyFacts := map[string]interface{}{
		"files_changed":   len(files),
		"additions":       adds,
		"deletions":       dels,
		"high_risk_files": len(highRisk),
	}

	suggestions := []string{
		"Review auth/session/runtime route changes first.",
		"Confirm generated files are not the source of truth.",
		"Run targeted tests before broad refactor.",
	}
	if len(highRisk) > 0 {
		suggestions = append([]string{"High-risk files detected — prioritize security review."}, suggestions...)
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(files)),
		KeyFacts:           keyFacts,
		FileList:           highRisk,
		Suggestions:        suggestions,
		BodyLines:          hunks,
	}
	return s, nil
}

func filterGitLog(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	var commits []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		commits = append(commits, line)
	}
	total := len(commits)
	if len(commits) > 20 {
		commits = commits[:20]
	}

	keyFacts := map[string]interface{}{
		"commits_shown": len(commits),
		"total_lines":   total,
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), total),
		KeyFacts:           keyFacts,
		BodyLines:          commits,
	}
	return s, nil
}

func computeReduction(raw, summaryItems int) float64 {
	if raw == 0 {
		return 0
	}
	est := summaryItems * 80
	if est < 0 {
		est = 0
	}
	red := float64(raw-est) / float64(raw)
	if red < 0 {
		return 0
	}
	if red > 0.99 {
		return 0.99
	}
	return red
}

func fallback(args []string, res *runner.Result) *output.Summary {
	lines := strings.Split(string(res.Stdout), "\n")
	total := len(lines)
	lines = safeTruncateLines(lines, 25, 25)
	return &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "low",
		EstimatedReduction: 0.0,
		KeyFacts: map[string]interface{}{
			"stdout_lines": total,
			"stderr_lines": len(strings.Split(string(res.Stderr), "\n")),
		},
		BodyLines: lines,
	}
}
