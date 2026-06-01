package runner

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/util"
)

type Result struct {
	Stdout     []byte
	Stderr     []byte
	ExitCode   int
	DurationMs int64
	RawLogPath string
}

type XitHome struct {
	Path string
}

func (h *XitHome) Ensure() error {
	dirs := []string{
		filepath.Join(h.Path, "runs"),
		filepath.Join(h.Path, "state"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

func (h *XitHome) RawLogPathFor(args []string, ts string) string {
	fname := fmt.Sprintf("%s-%s.raw.log", ts, util.CommandSlug(args))
	return filepath.Join(h.Path, "runs", fname)
}

func (h *XitHome) SaveRawAt(path string, args []string, r *Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("# command: %s\n", strings.Join(args, " ")))
	b.WriteString(fmt.Sprintf("# exit_code: %d\n", r.ExitCode))
	b.WriteString(fmt.Sprintf("# duration_ms: %d\n\n", r.DurationMs))
	b.Write(r.Stdout)
	if len(r.Stderr) > 0 {
		b.WriteString("\n# stderr\n")
		b.Write(r.Stderr)
	}

	if err := os.WriteFile(path, b.Bytes(), 0644); err != nil {
		return err
	}
	r.RawLogPath = path
	return nil
}

func (h *XitHome) SaveRaw(args []string, r *Result) error {
	ts := util.TimestampSlug()
	return h.SaveRawAt(h.RawLogPathFor(args, ts), args, r)
}

func Run(args []string) (*Result, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no command provided")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	res := &Result{
		Stdout:     stdout.Bytes(),
		Stderr:     stderr.Bytes(),
		DurationMs: duration.Milliseconds(),
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
	} else if err != nil {
		res.ExitCode = 1
	}
	return res, nil
}
