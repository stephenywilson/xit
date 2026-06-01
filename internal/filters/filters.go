package filters

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/history"
	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
)

type FilterFunc func(args []string, res *runner.Result) (*output.Summary, error)

type Dispatcher struct {
	filters map[string]FilterFunc
}

func NewDispatcher() *Dispatcher {
	d := &Dispatcher{filters: make(map[string]FilterFunc)}
	d.registerAll()
	return d
}

func (d *Dispatcher) registerAll() {
	d.filters["git"] = filterGit
	d.filters["rg"] = filterSearch
	d.filters["grep"] = filterSearch
	d.filters["find"] = filterFiles
	d.filters["ls"] = filterFiles
	d.filters["cat"] = filterRead
	d.filters["head"] = filterRead
	d.filters["tail"] = filterRead
	d.filters["npm"] = filterTest
	d.filters["pnpm"] = filterTest
	d.filters["pytest"] = filterTest
	d.filters["go"] = filterTest
	d.filters["cargo"] = filterTest
	d.filters["tsc"] = filterLint
	d.filters["eslint"] = filterLint
	d.filters["docker"] = filterDocker
	d.filters["jq"] = filterJSONLog
}

func tupleKey(args []string) string {
	if len(args) == 0 {
		return ""
	}
	if len(args) == 1 {
		return args[0]
	}
	return args[0] + " " + args[1]
}

func (d *Dispatcher) Dispatch(args []string, res *runner.Result) (s *output.Summary, err error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	// Panic recovery: any filter panic becomes a safe fallback summary.
	defer func() {
		if r := recover(); r != nil {
			s = safeFallback(args, res)
			err = nil
		}
	}()

	// Normalize absolute paths to binary names for routing.
	normArgs := make([]string, len(args))
	copy(normArgs, args)
	normArgs[0] = filepath.Base(normArgs[0])

	first := normArgs[0]
	tk := tupleKey(normArgs)

	filterName := "fallback"

	// Tuple-based routing for commands where first word alone is insufficient
	switch tk {
	case "git status", "git diff", "git log":
		s, err = filterGit(normArgs, res)
		filterName = "git"
	case "go test", "cargo test", "npm test", "pnpm test", "pytest test":
		s, err = filterTest(normArgs, res)
		filterName = "test"
	case "docker ps", "docker logs":
		s, err = filterDocker(normArgs, res)
		filterName = "docker"
	default:
		// Fallback to first-word routing for well-scoped commands
		switch first {
		case "rg", "grep":
			s, err = filterSearch(normArgs, res)
			filterName = "search"
		case "find", "ls":
			s, err = filterFiles(normArgs, res)
			filterName = "files"
		case "cat", "head", "tail":
			s, err = filterRead(normArgs, res)
			filterName = "read"
		case "tsc", "eslint":
			s, err = filterLint(normArgs, res)
			filterName = "lint"
		case "jq":
			s, err = filterJSONLog(normArgs, res)
			filterName = "jsonlog"
		default:
			s, err = d.fallback(normArgs, res)
		}
	}

	if s != nil {
		s.Filter = filterName
		s.Confidence = adjustConfidence(s.Confidence, res)
		s.Policy = ClassifyPolicy(normArgs)
	}
	return s, err
}

func (d *Dispatcher) fallback(args []string, res *runner.Result) (*output.Summary, error) {
	return safeFallback(args, res), nil
}

func safeFallback(args []string, res *runner.Result) *output.Summary {
	lines := strings.Split(string(res.Stdout), "\n")
	total := len(lines)
	lines = safeTruncateLines(lines, 25, 25)
	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "low",
		EstimatedReduction: 0.0,
		Policy:             ClassifyPolicy(args),
		KeyFacts: map[string]interface{}{
			"stdout_lines": total,
			"stderr_lines": len(strings.Split(string(res.Stderr), "\n")),
		},
		BodyLines: lines,
	}
	return s
}

func safeTruncateLines(lines []string, head, tail int) []string {
	if len(lines) <= head+tail {
		return lines
	}
	result := make([]string, 0, head+tail+1)
	result = append(result, lines[:head]...)
	result = append(result, "... [truncated] ...")
	result = append(result, lines[len(lines)-tail:]...)
	return result
}

func safeStringSlice(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func (d *Dispatcher) WriteHistory(home string, args []string, res *runner.Result, s *output.Summary) error {
	return d.WriteHistoryWithSummaryBytes(home, args, res, s, len([]byte(s.Render("human"))))
}

func (d *Dispatcher) WriteHistoryWithSummaryBytes(home string, args []string, res *runner.Result, s *output.Summary, summaryBytes int) error {
	r := history.Record{
		Timestamp:          time.Now().Format(time.RFC3339),
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		RawBytes:           len(res.Stdout) + len(res.Stderr),
		SummaryBytes:       summaryBytes,
		EstimatedReduction: s.EstimatedReduction,
		DurationMs:         res.DurationMs,
		Filter:             s.Filter,
		Confidence:         s.Confidence,
		Policy:             s.Policy,
		RawLog:             res.RawLogPath,
	}
	return history.Append(home, r)
}

func xitHome() string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	return filepath.Join(wd, ".xit")
}

func adjustConfidence(base string, res *runner.Result) string {
	if res.ExitCode == 0 {
		return base
	}
	switch base {
	case "high":
		return "medium"
	case "medium":
		return "low"
	default:
		return "low"
	}
}

// isDiagnosticFind returns true for find commands that search only limited
// system paths (e.g. tool discovery). These are low-output diagnostic commands
// and should not be counted as missed high-noise.
func isDiagnosticFind(args []string) bool {
	if len(args) < 2 || args[0] != "find" {
		return false
	}
	limitedPaths := map[string]bool{
		"/opt/homebrew":     true,
		"/usr/local":        true,
		"/usr/local/bin":    true,
		"/opt/homebrew/bin": true,
		"/usr/bin":          true,
		"/bin":              true,
	}
	hasLimitedPath := false
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			break
		}
		if !limitedPaths[arg] {
			return false
		}
		hasLimitedPath = true
	}
	return hasLimitedPath
}

// ClassifyPolicy returns the compression policy for a command:
//
//	should_compress   — high-output commands that clearly benefit from XiT
//	should_passthrough — short-output commands where compression adds little value
//	needs_review      — edge cases or unknown commands
func ClassifyPolicy(args []string) string {
	if len(args) == 0 {
		return "needs_review"
	}
	if isDiagnosticFind(args) {
		return "needs_review"
	}
	tk := tupleKey(args)
	switch tk {
	case "go test", "cargo test", "npm test", "pnpm test", "pytest test":
		return "should_compress"
	case "git diff", "git log":
		return "should_compress"
	case "git status", "git branch":
		return "should_passthrough"
	case "docker logs":
		return "should_compress"
	case "docker ps":
		return "should_passthrough"
	}
	switch args[0] {
	case "rg", "grep", "find", "cat", "head", "tail", "tsc", "eslint", "jq":
		return "should_compress"
	case "ls":
		return "should_passthrough"
	case "go", "npm", "pnpm", "cargo", "docker":
		return "needs_review"
	default:
		return "needs_review"
	}
}
