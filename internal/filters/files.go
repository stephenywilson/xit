package filters

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
)

var ignoredDirs = []string{
	"node_modules", ".git", "target", "dist", "build",
	"__pycache__", ".venv", ".idea", ".vscode", "vendor",
}

func shouldIgnoreDir(p string) bool {
	parts := strings.Split(p, string(filepath.Separator))
	for _, part := range parts {
		for _, ig := range ignoredDirs {
			if part == ig {
				return true
			}
		}
	}
	return false
}

func filterFiles(args []string, res *runner.Result) (*output.Summary, error) {
	lines := strings.Split(string(res.Stdout), "\n")
	dirs := make(map[string]int)
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if shouldIgnoreDir(line) {
			continue
		}
		files = append(files, line)
		dir := filepath.Dir(line)
		if dir == "." {
			dir = "root"
		}
		dirs[dir]++
	}

	var body []string
	if len(dirs) > 0 {
		body = append(body, "Directory summary:")
		for d, c := range dirs {
			body = append(body, fmt.Sprintf("  %s: %d items", d, c))
		}
	}
	if len(files) <= 20 {
		body = append(body, "Files:")
		for _, f := range files {
			body = append(body, "  "+f)
		}
	} else {
		body = append(body, fmt.Sprintf("... %d total files ...", len(files)))
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(len(res.Stdout)+len(res.Stderr), len(body)),
		KeyFacts: map[string]interface{}{
			"total_files": len(files),
			"directories": len(dirs),
		},
		BodyLines: body,
	}
	return s, nil
}

var skeletonRe = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(import|from)\s+`),
	regexp.MustCompile(`^\s*(func|class|interface|type|struct|enum|const|let|var|def)\s+`),
	regexp.MustCompile(`^\s*(export|public|private|protected)\s+`),
	regexp.MustCompile(`^\s*[#%]`),
}

func filterRead(args []string, res *runner.Result) (*output.Summary, error) {
	content := string(res.Stdout)
	lines := strings.Split(content, "\n")
	size := len(res.Stdout)

	var body []string
	if size < 5000 && len(lines) < 100 {
		// Small file: output directly
		body = lines
	} else {
		body = append(body, fmt.Sprintf("File size: %d bytes, lines: %d", size, len(lines)))
		// Head
		if len(lines) >= 20 {
			body = append(body, "--- head ---")
			body = append(body, lines[:20]...)
		}
		// Skeleton
		var skeleton []string
		for _, line := range lines {
			for _, re := range skeletonRe {
				if re.MatchString(line) {
					skeleton = append(skeleton, line)
					break
				}
			}
		}
		if len(skeleton) > 0 {
			body = append(body, "--- skeleton ---")
			for _, s := range skeleton {
				body = append(body, s)
			}
		}
		// Tail
		if len(lines) >= 20 {
			body = append(body, "--- tail ---")
			body = append(body, lines[len(lines)-20:]...)
		}
	}

	s := &output.Summary{
		Command:            strings.Join(args, " "),
		ExitCode:           res.ExitCode,
		DurationMs:         res.DurationMs,
		RawLogPath:         res.RawLogPath,
		Confidence:         "high",
		EstimatedReduction: computeReduction(size, len(body)),
		KeyFacts: map[string]interface{}{
			"bytes": size,
			"lines": len(lines),
		},
		BodyLines: body,
	}
	return s, nil
}
