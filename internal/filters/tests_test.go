package filters

import (
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/runner"
)

func TestFilterTestPass(t *testing.T) {
	input := `PASS
Tests: 5 passed, 5 total
`
	res := &runner.Result{Stdout: []byte(input), ExitCode: 0, DurationMs: 100}
	s, err := filterTest([]string{"go", "test"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if s.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", s.ExitCode)
	}
	render := s.Render("human")
	if !strings.Contains(render, "passed") {
		t.Error("expected pass summary in output")
	}
}

func TestFilterTestFail(t *testing.T) {
	input := `FAIL
Tests: 1 failed, 4 passed, 5 total
Error: something broke
    at file.go:42
    at file.go:50
`
	res := &runner.Result{Stdout: []byte(input), ExitCode: 1, DurationMs: 100}
	s, err := filterTest([]string{"npm", "test"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if s.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", s.ExitCode)
	}
	render := s.Render("human")
	if !strings.Contains(render, "Failed") {
		t.Error("expected failed tests section")
	}
}
