package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/history"
)

func TestNewCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, []string{"bash"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if s.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if s.Command != "bash" {
		t.Fatalf("expected command bash, got %s", s.Command)
	}

	for _, name := range []string{"meta.json", "history.jsonl", "raw.log"} {
		path := filepath.Join(s.Dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(s.Dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("invalid meta.json: %v", err)
	}
	if meta.Command != "bash" {
		t.Errorf("meta command mismatch: %s", meta.Command)
	}
}

func TestEnviron(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	env := s.Environ()
	var hasID, hasDir bool
	for _, e := range env {
		if strings.HasPrefix(e, "XIT_SESSION_ID=") {
			hasID = true
		}
		if strings.HasPrefix(e, "XIT_SESSION_DIR=") {
			hasDir = true
		}
	}
	if !hasID {
		t.Error("missing XIT_SESSION_ID")
	}
	if !hasDir {
		t.Error("missing XIT_SESSION_DIR")
	}
}

func TestStartBannerHuman(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	b := s.StartBanner("human")
	if !strings.Contains(b, s.ID) {
		t.Error("banner missing session ID")
	}
	if !strings.Contains(b, "bash") {
		t.Error("banner missing command")
	}
	if !strings.Contains(b, "Session Mode") {
		t.Error("human banner missing 'Session Mode'")
	}
}

func TestStartBannerAgent(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	b := s.StartBanner("agent")
	if !strings.HasPrefix(b, "[xit:session start") {
		t.Errorf("agent banner unexpected: %s", b)
	}
	if !strings.Contains(b, s.ID) {
		t.Error("agent banner missing session ID")
	}
	if !strings.Contains(b, "bash") {
		t.Error("agent banner missing command")
	}
}

func TestStartBannerJSON(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	b := s.StartBanner("json")
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(b), &data); err != nil {
		t.Fatalf("invalid json banner: %v", err)
	}
	if data["event"] != "session_start" {
		t.Errorf("unexpected event: %v", data["event"])
	}
}

func TestEndReportHumanEmptyHistory(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	r := s.EndReport("human", 0)
	if !strings.Contains(r, "commands") {
		t.Error("human report missing commands")
	}
	if !strings.Contains(r, s.ID) {
		t.Error("human report missing session ID")
	}
	if !strings.Contains(r, "no explicit xit-wrapped commands") {
		t.Error("human report missing empty-history note")
	}
}

func TestEndReportHumanWithHistory(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	s.AppendHistory(history.Record{Command: "git status", ExitCode: 0, RawBytes: 1000, SummaryBytes: 100})
	s.AppendHistory(history.Record{Command: "go test", ExitCode: 0, RawBytes: 2000, SummaryBytes: 200})

	r := s.EndReport("human", 0)
	if !strings.Contains(r, "commands   : 2") {
		t.Errorf("human report commands mismatch:\n%s", r)
	}
	if !strings.Contains(r, "raw output : 2.9 kB") {
		t.Errorf("human report raw bytes mismatch:\n%s", r)
	}
	if !strings.Contains(r, "compressed : 300 B") {
		t.Errorf("human report summary bytes mismatch:\n%s", r)
	}
	if !strings.Contains(r, "reduction  : 90.0%") {
		t.Errorf("human report reduction mismatch:\n%s", r)
	}
	if !strings.Contains(r, "saved      : ~675 estimated tokens") {
		t.Errorf("human report saved tokens mismatch:\n%s", r)
	}
}

func TestEndReportAgent(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	r := s.EndReport("agent", 0)
	if !strings.HasPrefix(r, "[xit:session end") {
		t.Errorf("agent report unexpected: %s", r)
	}
}

func TestEndReportJSON(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	s.AppendHistory(history.Record{Command: "git status", ExitCode: 0, RawBytes: 1000, SummaryBytes: 100})

	r := s.EndReport("json", 0)
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(r), &data); err != nil {
		t.Fatalf("invalid json report: %v", err)
	}
	if data["event"] != "session_end" {
		t.Errorf("unexpected event: %v", data["event"])
	}
	if data["commands"] != float64(1) {
		t.Errorf("unexpected commands: %v", data["commands"])
	}
	if data["raw_bytes"] != float64(1000) {
		t.Errorf("unexpected raw_bytes: %v", data["raw_bytes"])
	}
	if data["summary_bytes"] != float64(100) {
		t.Errorf("unexpected summary_bytes: %v", data["summary_bytes"])
	}
}

func TestAppendHistory(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir, []string{"bash"})
	r := history.Record{
		Command:  "git status",
		ExitCode: 0,
		RawBytes: 100,
	}
	if err := s.AppendHistory(r); err != nil {
		t.Fatalf("AppendHistory failed: %v", err)
	}
	if err := s.AppendHistory(r); err != nil {
		t.Fatalf("AppendHistory failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(s.Dir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 history lines, got %d", len(lines))
	}
}

func TestSessionIDUniqueWithinSameSecond(t *testing.T) {
	dir := t.TempDir()
	ids := make(map[string]bool)
	for i := 0; i < 5; i++ {
		s, err := New(dir, []string{"bash"})
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if ids[s.ID] {
			t.Fatalf("duplicate session ID: %s", s.ID)
		}
		ids[s.ID] = true
	}
}

func TestSessionDoesNotOverwriteExistingDir(t *testing.T) {
	dir := t.TempDir()
	s1, err := New(dir, []string{"bash"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Create a marker file inside the first session dir.
	marker := filepath.Join(s1.Dir, "marker.txt")
	if err := os.WriteFile(marker, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create another session with the same command in rapid succession.
	s2, err := New(dir, []string{"bash"})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if s1.Dir == s2.Dir {
		t.Fatalf("session directories should differ: %s", s1.Dir)
	}

	// Verify the first session marker is untouched.
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker file missing: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("marker file was overwritten")
	}
}

func TestSessionIDSlugifiesTarget(t *testing.T) {
	dir := t.TempDir()
	cases := [][]string{
		{"bash"},
		{"/usr/bin/bash"},
		{"echo hello world"},
		{"node_modules/.bin/tsc"},
		{"claude --mode agent"},
	}
	for _, cmd := range cases {
		s, err := New(dir, cmd)
		if err != nil {
			t.Fatalf("New failed for %v: %v", cmd, err)
		}
		if strings.Contains(s.ID, "/") {
			t.Errorf("session ID contains slash: %s", s.ID)
		}
		if strings.Contains(s.ID, " ") {
			t.Errorf("session ID contains space: %s", s.ID)
		}
		// Verify the directory exists and matches ID.
		if _, err := os.Stat(s.Dir); err != nil {
			t.Errorf("session dir missing: %s", s.Dir)
		}
	}
}
