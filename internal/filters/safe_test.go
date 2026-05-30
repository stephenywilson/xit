package filters

import (
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/output"
	"github.com/stephenywilson/xit/internal/runner"
)

func TestSafeTruncateLinesNoOp(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := safeTruncateLines(lines, 25, 25)
	if len(got) != 3 {
		t.Errorf("expected 3 lines, got %d", len(got))
	}
}

func TestSafeTruncateLinesTruncates(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	got := safeTruncateLines(lines, 10, 10)
	if len(got) != 21 {
		t.Errorf("expected 21 lines (10 head + marker + 10 tail), got %d", len(got))
	}
	if got[10] != "... [truncated] ..." {
		t.Errorf("expected truncation marker at index 10, got %q", got[10])
	}
}

func TestSafeStringSliceNoOp(t *testing.T) {
	if safeStringSlice("hi", 10) != "hi" {
		t.Error("expected no-op for short string")
	}
}

func TestSafeStringSliceTruncates(t *testing.T) {
	if safeStringSlice("hello world", 5) != "hello" {
		t.Error("expected truncation")
	}
}

func TestFallbackDoesNotPanicWithManyLines(t *testing.T) {
	// Reproduce the original panic: >50 lines caused slice bounds out of range.
	stdout := strings.Repeat("line\n", 100)
	res := &runner.Result{Stdout: []byte(stdout), ExitCode: 0, DurationMs: 1}
	d := NewDispatcher()
	s, err := d.fallback([]string{"foo"}, res)
	if err != nil {
		t.Fatalf("fallback error: %v", err)
	}
	if len(s.BodyLines) != 51 {
		t.Errorf("expected 51 body lines, got %d", len(s.BodyLines))
	}
}

func TestDispatchRecoversFromPanic(t *testing.T) {
	// Register a filter that panics.
	d := NewDispatcher()
	d.filters["panic"] = func(args []string, res *runner.Result) (*output.Summary, error) {
		panic("intentional test panic")
	}
	// Route to the panic filter via first-word matching.
	res := &runner.Result{Stdout: []byte("ok\n"), ExitCode: 0, DurationMs: 1}
	s, err := d.Dispatch([]string{"panic"}, res)
	if err != nil {
		t.Fatalf("expected no error after recovery, got: %v", err)
	}
	if s == nil {
		t.Fatal("expected summary after recovery, got nil")
	}
	if s.Filter != "fallback" {
		t.Errorf("expected filter fallback after recovery, got %q", s.Filter)
	}
	if s.ExitCode != 0 {
		t.Errorf("expected exit code 0 preserved, got %d", s.ExitCode)
	}
}

func TestDispatchNormalizesAbsolutePath(t *testing.T) {
	d := NewDispatcher()
	res := &runner.Result{Stdout: []byte("ok\n"), ExitCode: 0, DurationMs: 1}
	// Pass absolute path to go binary; should still route to test filter.
	s, err := d.Dispatch([]string{"/usr/local/go/bin/go", "test", "-v", "./..."}, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Filter != "test" {
		t.Errorf("expected filter test for absolute go path, got %q", s.Filter)
	}
	if !strings.HasPrefix(s.Command, "go ") {
		t.Errorf("expected normalized command starting with 'go ', got %q", s.Command)
	}
}

func TestDispatchNormalizesAbsolutePathGit(t *testing.T) {
	d := NewDispatcher()
	res := &runner.Result{Stdout: []byte("ok\n"), ExitCode: 0, DurationMs: 1}
	s, err := d.Dispatch([]string{"/usr/bin/git", "status"}, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Filter != "git" {
		t.Errorf("expected filter git for absolute git path, got %q", s.Filter)
	}
}

func TestSafeFallbackPreservesExitCodeAndRawLog(t *testing.T) {
	res := &runner.Result{
		Stdout:     []byte("some output\n"),
		Stderr:     []byte("err\n"),
		ExitCode:   42,
		DurationMs: 7,
		RawLogPath: "/tmp/test.raw.log",
	}
	s := safeFallback([]string{"cmd"}, res)
	if s.ExitCode != 42 {
		t.Errorf("exit code = %d, want 42", s.ExitCode)
	}
	if s.RawLogPath != "/tmp/test.raw.log" {
		t.Errorf("raw log = %q, want /tmp/test.raw.log", s.RawLogPath)
	}
	if s.DurationMs != 7 {
		t.Errorf("duration = %d, want 7", s.DurationMs)
	}
}
