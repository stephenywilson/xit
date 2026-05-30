package filters

import (
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/runner"
)

func TestClassifierTuple(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantFilter string
	}{
		{"go test", []string{"go", "test"}, "test"},
		{"go run", []string{"go", "run", "main.go"}, "fallback"},
		{"go build", []string{"go", "build"}, "fallback"},
		{"go version", []string{"go", "version"}, "fallback"},
		{"cargo test", []string{"cargo", "test"}, "test"},
		{"cargo build", []string{"cargo", "build"}, "fallback"},
		{"npm test", []string{"npm", "test"}, "test"},
		{"npm install", []string{"npm", "install"}, "fallback"},
		{"pnpm test", []string{"pnpm", "test"}, "test"},
		{"pnpm install", []string{"pnpm", "install"}, "fallback"},
		{"git diff", []string{"git", "diff"}, "git"},
		{"git status", []string{"git", "status"}, "git"},
		{"git log", []string{"git", "log"}, "git"},
		{"git branch", []string{"git", "branch"}, "fallback"},
		{"rg", []string{"rg", "func"}, "search"},
		{"grep", []string{"grep", "func", "."}, "search"},
		{"docker ps", []string{"docker", "ps"}, "docker"},
		{"docker logs", []string{"docker", "logs", "app"}, "docker"},
		{"docker build", []string{"docker", "build", "."}, "fallback"},
		{"find", []string{"find", ".", "-name", "*.go"}, "files"},
		{"cat", []string{"cat", "main.go"}, "read"},
		{"tsc", []string{"tsc"}, "lint"},
		{"eslint", []string{"eslint", "."}, "lint"},
		{"unknown", []string{"foobar"}, "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDispatcher()
			res := &runner.Result{Stdout: []byte("test output\n"), ExitCode: 0, DurationMs: 1}
			s, err := d.Dispatch(tt.args, res)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.Filter != tt.wantFilter {
				t.Errorf("filter = %q, want %q", s.Filter, tt.wantFilter)
			}
		})
	}
}

func TestFilterErrorFallback(t *testing.T) {
	// Verify that when a filter returns an error, main.go shows raw stdout/stderr
	// and preserves exit code. We simulate this by calling fallback directly.
	d := NewDispatcher()
	res := &runner.Result{Stdout: []byte("hello\n"), Stderr: []byte("err\n"), ExitCode: 1, DurationMs: 1}
	s, err := d.fallback([]string{"unknown"}, res)
	if err != nil {
		t.Fatalf("fallback error: %v", err)
	}
	if s.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", s.ExitCode)
	}
	render := s.Render("human")
	if !strings.Contains(render, "hello") {
		t.Error("expected raw stdout content in fallback")
	}
}

func TestClassifyPolicy(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"go test", []string{"go", "test"}, "should_compress"},
		{"go build", []string{"go", "build"}, "needs_review"},
		{"git diff", []string{"git", "diff"}, "should_compress"},
		{"git status", []string{"git", "status"}, "should_passthrough"},
		{"git branch", []string{"git", "branch"}, "should_passthrough"},
		{"rg", []string{"rg", "func"}, "should_compress"},
		{"ls", []string{"ls", "-la"}, "should_passthrough"},
		{"docker logs", []string{"docker", "logs", "app"}, "should_compress"},
		{"docker ps", []string{"docker", "ps"}, "should_passthrough"},
		{"npm install", []string{"npm", "install"}, "needs_review"},
		{"unknown", []string{"foobar"}, "needs_review"},
		{"empty", []string{}, "needs_review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPolicy(tt.args)
			if got != tt.want {
				t.Errorf("ClassifyPolicy(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestSearchPatternParsing(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
	}{
		{"rg func", []string{"rg", "func"}, "func"},
		{"rg -n func", []string{"rg", "-n", "func"}, "func"},
		{"rg --include=*.go func", []string{"rg", "--include=*.go", "func"}, "func"},
		{"grep -R func .", []string{"grep", "-R", "func", "."}, "func"},
		{"grep only flags", []string{"grep", "-R", "-i"}, "unknown"},
		{"no args", []string{"rg"}, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPattern(tt.args)
			if got != tt.want {
				t.Errorf("extractPattern(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
