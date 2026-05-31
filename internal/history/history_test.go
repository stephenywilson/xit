package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndGain(t *testing.T) {
	dir := t.TempDir()
	r1 := Record{
		Timestamp:          "2026-01-01T00:00:00Z",
		Command:            "git status",
		ExitCode:           0,
		RawBytes:           1000,
		SummaryBytes:       200,
		EstimatedReduction: 0.8,
		DurationMs:         10,
		Filter:             "git_status",
		Confidence:         "high",
		RawLog:             ".xit/runs/1.raw.log",
	}
	r2 := Record{
		Timestamp:    "2026-01-01T01:00:00Z",
		Command:      "git diff",
		ExitCode:     0,
		RawBytes:     2000,
		SummaryBytes: 300,
		DurationMs:   20,
		Filter:       "git_diff",
		Confidence:   "high",
		RawLog:       ".xit/runs/2.raw.log",
	}

	if err := Append(dir, r1); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, r2); err != nil {
		t.Fatal(err)
	}

	g, warnings, err := ComputeGain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if g.TotalCommands != 2 {
		t.Errorf("commands = %d, want 2", g.TotalCommands)
	}
	if g.TotalRawBytes != 3000 {
		t.Errorf("raw = %d, want 3000", g.TotalRawBytes)
	}
	if g.TotalSummaryBytes != 500 {
		t.Errorf("summary = %d, want 500", g.TotalSummaryBytes)
	}
	if g.EstimatedSavedBytes != 2500 {
		t.Errorf("saved = %d, want 2500", g.EstimatedSavedBytes)
	}
}

func TestBenchReport(t *testing.T) {
	dir := t.TempDir()
	records := []Record{
		{Command: "go test -v ./...", RawBytes: 10000, SummaryBytes: 500, Filter: "test", Confidence: "high", Policy: "should_compress"},
		{Command: "git status", RawBytes: 200, SummaryBytes: 180, Filter: "git", Confidence: "high", Policy: "should_passthrough"},
		{Command: "grep foo", RawBytes: 5000, SummaryBytes: 2000, Filter: "search", Confidence: "medium", Policy: "should_compress"},
	}
	for _, r := range records {
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}

	br, err := ComputeBenchReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if br.TotalCommands != 3 {
		t.Errorf("commands = %d, want 3", br.TotalCommands)
	}
	if br.OverallReduction <= 0 {
		t.Errorf("expected positive overall reduction, got %f", br.OverallReduction)
	}
	if len(br.ByFilter) != 3 {
		t.Errorf("expected 3 filter groups, got %d", len(br.ByFilter))
	}
	if len(br.ByPolicy) != 2 {
		t.Errorf("expected 2 policy groups, got %d", len(br.ByPolicy))
	}
	if len(br.ByConfidence) != 2 {
		t.Errorf("expected 2 confidence groups, got %d", len(br.ByConfidence))
	}
	if br.ByPolicy["should_compress"] == nil {
		t.Error("expected should_compress policy group")
	}
	if br.ByPolicy["should_passthrough"] == nil {
		t.Error("expected should_passthrough policy group")
	}
}

func TestRawLogPathExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs", "test.raw.log")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want hello", string(data))
	}
}
