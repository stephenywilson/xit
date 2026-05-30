package hitrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		input    string
		wantNorm string
		wantWrap bool
	}{
		{"go test -v ./...", "go test -v ./...", false},
		{"xit auto go test -v ./...", "go test -v ./...", true},
		{"./xit auto go test -v ./...", "go test -v ./...", true},
		{"xit auto\tgo test -v ./...", "go test -v ./...", true},
		{"git status", "git status", false},
		{"/usr/local/bin/git status", "git status", false},
		{"xit auto git status", "git status", true},
		{"", "", false},
		{"   ", "", false},
	}
	for _, tt := range tests {
		norm, wrapped := normalizeCommand(tt.input)
		if norm != tt.wantNorm || wrapped != tt.wantWrap {
			t.Errorf("normalizeCommand(%q) = (%q, %v), want (%q, %v)", tt.input, norm, wrapped, tt.wantNorm, tt.wantWrap)
		}
	}
}

func TestComputeReportHistoryOnly(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")
	_ = os.MkdirAll(projectHome, 0755)
	_ = os.MkdirAll(userHome, 0755)

	// Write a history record without policy (should fallback to needs_review).
	histPath := filepath.Join(projectHome, "history.jsonl")
	rec := `{"timestamp":"` + time.Now().Format(time.RFC3339) + `","command":"go test -v ./...","exit_code":0,"raw_bytes":1000,"summary_bytes":200,"estimated_reduction":0.8,"duration_ms":100,"filter":"test","confidence":"high","raw_log":"/tmp/fake.raw.log"}` + "\n"
	_ = os.WriteFile(histPath, []byte(rec), 0644)

	report, err := ComputeReport(userHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.Mode != "history_only" {
		t.Errorf("mode = %q, want history_only", report.Mode)
	}
	if report.SummaryFidelity.XitAutoRuns != 1 {
		t.Errorf("xit_auto_runs = %d, want 1", report.SummaryFidelity.XitAutoRuns)
	}
	if report.ShellCommandsSeen != 0 {
		t.Errorf("shell_commands_seen = %d, want 0", report.ShellCommandsSeen)
	}
	if report.Verdict != "partial" {
		t.Errorf("verdict = %q, want partial", report.Verdict)
	}
}

func TestComputeReportWithHooks(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")
	userXiTHome := filepath.Join(userHome, ".xit")
	_ = os.MkdirAll(filepath.Join(userXiTHome, "kimi-hooks"), 0755)
	_ = os.MkdirAll(projectHome, 0755)

	// Hook event: missed high-noise (go test not wrapped).
	now := time.Now().Format(time.RFC3339)
	events := `{"time":"` + now + `","original_command":"go test -v ./...","action":"observe"}` + "\n"
	// Hook event: correct passthrough (git status not wrapped).
	events += `{"time":"` + now + `","original_command":"git status","action":"observe"}` + "\n"
	// Hook event: false positive (git status wrapped).
	events += `{"time":"` + now + `","original_command":"xit auto git status","action":"observe"}` + "\n"
	// Hook event: correctly wrapped.
	events += `{"time":"` + now + `","original_command":"xit auto go test -v ./...","action":"observe"}` + "\n"
	// Hook event: needs_review.
	events += `{"time":"` + now + `","original_command":"some_unknown_tool arg","action":"observe"}` + "\n"

	_ = os.WriteFile(filepath.Join(userXiTHome, "kimi-hooks", "events.jsonl"), []byte(events), 0644)

	report, err := ComputeReport(userXiTHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.Mode != "hook_events_plus_history" {
		t.Errorf("mode = %q, want hook_events_plus_history", report.Mode)
	}
	if report.ShellCommandsSeen != 5 {
		t.Errorf("shell_commands_seen = %d, want 5", report.ShellCommandsSeen)
	}
	if report.ShouldCompress.Total != 2 {
		t.Errorf("should_compress.total = %d, want 2", report.ShouldCompress.Total)
	}
	if report.ShouldCompress.CorrectlyWrapped != 1 {
		t.Errorf("should_compress.correctly_wrapped = %d, want 1", report.ShouldCompress.CorrectlyWrapped)
	}
	if report.ShouldCompress.Missed != 1 {
		t.Errorf("should_compress.missed = %d, want 1", report.ShouldCompress.Missed)
	}
	if len(report.MissedHighNoise) != 1 || report.MissedHighNoise[0] != "go test -v ./..." {
		t.Errorf("missed_high_noise = %v, want [go test -v ./...]", report.MissedHighNoise)
	}
	if len(report.MissedHighNoiseTop) != 1 || report.MissedHighNoiseTop[0].Command != "go test -v ./..." || report.MissedHighNoiseTop[0].Count != 1 {
		t.Errorf("missed_high_noise_top = %v, want [{go test -v ./... 1}]", report.MissedHighNoiseTop)
	}
	if report.ShouldPassthrough.Total != 2 {
		t.Errorf("should_passthrough.total = %d, want 2", report.ShouldPassthrough.Total)
	}
	if report.ShouldPassthrough.CorrectlyPassthrough != 1 {
		t.Errorf("should_passthrough.correctly_passthrough = %d, want 1", report.ShouldPassthrough.CorrectlyPassthrough)
	}
	if report.ShouldPassthrough.FalsePositive != 1 {
		t.Errorf("should_passthrough.false_positive = %d, want 1", report.ShouldPassthrough.FalsePositive)
	}
	if len(report.FalsePositive) != 1 || report.FalsePositive[0] != "xit auto git status" {
		t.Errorf("false_positive = %v, want [xit auto git status]", report.FalsePositive)
	}
	if len(report.NeedsReview) != 1 {
		t.Errorf("needs_review count = %d, want 1", len(report.NeedsReview))
	}
	if report.Verdict != "fail" {
		t.Errorf("verdict = %q, want fail (compress_recall 50%% < 90%%)", report.Verdict)
	}
}

func TestComputeReportMalformedEvent(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")
	userXiTHome := filepath.Join(userHome, ".xit")
	_ = os.MkdirAll(filepath.Join(userXiTHome, "kimi-hooks"), 0755)

	events := `not json` + "\n"
	events += `{"time":"` + time.Now().Format(time.RFC3339) + `","original_command":"go test","action":"observe"}` + "\n"
	_ = os.WriteFile(filepath.Join(userXiTHome, "kimi-hooks", "events.jsonl"), []byte(events), 0644)

	report, err := ComputeReport(userXiTHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.MalformedEvents != 1 {
		t.Errorf("malformed_events = %d, want 1", report.MalformedEvents)
	}
	if report.ShellCommandsSeen != 1 {
		t.Errorf("shell_commands_seen = %d, want 1", report.ShellCommandsSeen)
	}
}

func TestComputeReportRawLogPresence(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")
	_ = os.MkdirAll(projectHome, 0755)

	// Create a real raw log file with go test signal.
	rawLogPath := filepath.Join(projectHome, "test.raw.log")
	_ = os.WriteFile(rawLogPath, []byte("# command: go test\n# exit_code: 0\nok  \tgithub.com/example/pkg\n"), 0644)

	histPath := filepath.Join(projectHome, "history.jsonl")
	rec := `{"timestamp":"` + time.Now().Format(time.RFC3339) + `","command":"go test -v ./...","exit_code":0,"raw_bytes":100,"summary_bytes":50,"estimated_reduction":0.5,"duration_ms":10,"filter":"test","confidence":"high","policy":"should_compress","raw_log":"` + rawLogPath + `"}` + "\n"
	_ = os.WriteFile(histPath, []byte(rec), 0644)

	report, err := ComputeReport(userHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.SummaryFidelity.RawLogPresent != 1 {
		t.Errorf("raw_log_present = %d, want 1", report.SummaryFidelity.RawLogPresent)
	}
	if !report.SummaryFidelity.PanicFree {
		t.Error("panic_free should be true")
	}
	if report.SummaryFidelity.FailureSignalPresent != 1 {
		t.Errorf("failure_signal_present = %d, want 1 (success counts as present)", report.SummaryFidelity.FailureSignalPresent)
	}
	if report.SummaryFidelity.CommandSpecificSignal != 1 {
		t.Errorf("command_specific_signal = %d, want 1", report.SummaryFidelity.CommandSpecificSignal)
	}
}

func TestComputeReportFailureSignal(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")
	_ = os.MkdirAll(projectHome, 0755)

	rawLogPath := filepath.Join(projectHome, "fail.raw.log")
	_ = os.WriteFile(rawLogPath, []byte("# command: go test\n# exit_code: 1\nFAIL\n"), 0644)

	histPath := filepath.Join(projectHome, "history.jsonl")
	rec := `{"timestamp":"` + time.Now().Format(time.RFC3339) + `","command":"go test -v ./...","exit_code":1,"raw_bytes":100,"summary_bytes":50,"estimated_reduction":0.5,"duration_ms":10,"filter":"test","confidence":"high","policy":"should_compress","raw_log":"` + rawLogPath + `"}` + "\n"
	_ = os.WriteFile(histPath, []byte(rec), 0644)

	report, err := ComputeReport(userHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.SummaryFidelity.FailureSignalPresent != 1 {
		t.Errorf("failure_signal_present = %d, want 1", report.SummaryFidelity.FailureSignalPresent)
	}
}

func TestComputeReportPanicDetection(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userHome := filepath.Join(tmp, "user")
	_ = os.MkdirAll(projectHome, 0755)

	rawLogPath := filepath.Join(projectHome, "panic.raw.log")
	_ = os.WriteFile(rawLogPath, []byte("panic: runtime error: index out of range\n"), 0644)

	histPath := filepath.Join(projectHome, "history.jsonl")
	rec := `{"timestamp":"` + time.Now().Format(time.RFC3339) + `","command":"go test","exit_code":2,"raw_bytes":100,"summary_bytes":50,"estimated_reduction":0.5,"duration_ms":10,"filter":"test","confidence":"high","policy":"should_compress","raw_log":"` + rawLogPath + `"}` + "\n"
	_ = os.WriteFile(histPath, []byte(rec), 0644)

	report, err := ComputeReport(userHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.SummaryFidelity.PanicFree {
		t.Error("panic_free should be false when raw log contains panic")
	}
}

func TestFormatReport(t *testing.T) {
	r := &Report{
		Window:            "last 2h",
		Mode:              "hook_events_plus_history",
		ShellCommandsSeen: 4,
		ShouldCompress: ShouldCompressStats{
			Total:            2,
			CorrectlyWrapped: 1,
			Missed:           1,
			CompressRecall:   50.0,
		},
		ShouldPassthrough: ShouldPassthroughStats{
			Total:                2,
			CorrectlyPassthrough: 2,
			FalsePositive:        0,
			PassthroughPrecision: 100.0,
		},
		SummaryFidelity: SummaryFidelityStats{
			XitAutoRuns:           1,
			RawLogPresent:         1,
			ExitCodePresent:       1,
			ReductionPresent:      1,
			FailureSignalPresent:  1,
			CommandSpecificSignal: 1,
			PanicFree:             true,
			BasicFidelity:         100.0,
		},
		MissedHighNoise: []string{"go test -v ./..."},
		FalsePositive:   []string{},
		Recommendations: []string{"strengthen go test verbose rule"},
		Targets: TargetStats{
			CompressRecallTarget:       "90%",
			PassthroughPrecisionTarget: "98%",
			SummaryFidelityTarget:      "95%",
		},
		Verdict: "fail",
	}
	out := FormatReport(r, false)
	if !strings.Contains(out, "XiT Kimi Routing Hit Rate") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "compress_recall: 50.0%") {
		t.Error("missing compress_recall")
	}
	if !strings.Contains(out, "verdict: fail") {
		t.Error("missing verdict")
	}
	if !strings.Contains(out, "targets:") {
		t.Error("missing targets")
	}
	if !strings.Contains(out, "go test -v ./...") {
		t.Error("missing missed command")
	}
}

func TestFormatReportHistoryOnly(t *testing.T) {
	r := &Report{
		Window: "last 2h",
		Mode:   "history_only",
	}
	out := FormatReport(r, false)
	if !strings.Contains(out, "warning: Kimi shell command events unavailable") {
		t.Error("missing history_only warning")
	}
}

func TestComputeReportForAdapterClaude(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userXiTHome := filepath.Join(tmp, "xit")
	_ = os.MkdirAll(filepath.Join(userXiTHome, "claude-hooks"), 0755)
	_ = os.MkdirAll(projectHome, 0755)

	now := time.Now().Format(time.RFC3339)
	// correctly_wrapped: xit auto go test -v ./...
	events := `{"time":"` + now + `","original_command":"xit auto go test -v ./...","action":"observe"}` + "\n"
	// correct_passthrough: git status
	events += `{"time":"` + now + `","original_command":"git status","action":"observe"}` + "\n"
	// missed_high_noise: go test -v ./... not wrapped
	events += `{"time":"` + now + `","original_command":"go test -v ./...","action":"observe"}` + "\n"
	// false_positive: xit auto git status
	events += `{"time":"` + now + `","original_command":"xit auto git status","action":"observe"}` + "\n"
	// correctly_wrapped: xit auto rg "TODO" .
	events += `{"time":"` + now + `","original_command":"xit auto rg \"TODO\" .","action":"observe"}` + "\n"
	// needs_review: unknown tool
	events += `{"time":"` + now + `","original_command":"some_unknown_tool arg","action":"observe"}` + "\n"
	_ = os.WriteFile(filepath.Join(userXiTHome, "claude-hooks", "events.jsonl"), []byte(events), 0644)

	report, err := ComputeReportForAdapter("claude", userXiTHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReportForAdapter error: %v", err)
	}
	if report.Adapter != "claude" {
		t.Errorf("adapter = %q, want claude", report.Adapter)
	}
	if report.Mode != "hook_events_plus_history" {
		t.Errorf("mode = %q, want hook_events_plus_history", report.Mode)
	}
	if report.ShellCommandsSeen != 6 {
		t.Errorf("shell_commands_seen = %d, want 6", report.ShellCommandsSeen)
	}
	if report.ShouldCompress.Total != 3 {
		t.Errorf("should_compress.total = %d, want 3", report.ShouldCompress.Total)
	}
	if report.ShouldCompress.CorrectlyWrapped != 2 {
		t.Errorf("should_compress.correctly_wrapped = %d, want 2", report.ShouldCompress.CorrectlyWrapped)
	}
	if report.ShouldCompress.Missed != 1 {
		t.Errorf("should_compress.missed = %d, want 1", report.ShouldCompress.Missed)
	}
	if report.ShouldPassthrough.Total != 2 {
		t.Errorf("should_passthrough.total = %d, want 2", report.ShouldPassthrough.Total)
	}
	if report.ShouldPassthrough.CorrectlyPassthrough != 1 {
		t.Errorf("should_passthrough.correctly_passthrough = %d, want 1", report.ShouldPassthrough.CorrectlyPassthrough)
	}
	if report.ShouldPassthrough.FalsePositive != 1 {
		t.Errorf("should_passthrough.false_positive = %d, want 1", report.ShouldPassthrough.FalsePositive)
	}
	if len(report.NeedsReview) != 1 {
		t.Errorf("needs_review count = %d, want 1", len(report.NeedsReview))
	}
}

func TestComputeReportForAdapterNoEvents(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userXiTHome := filepath.Join(tmp, "xit")
	_ = os.MkdirAll(projectHome, 0755)
	// No claude-hooks directory → events file missing

	report, err := ComputeReportForAdapter("claude", userXiTHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Verdict != "partial" {
		t.Errorf("verdict = %q, want partial (no events)", report.Verdict)
	}
	if report.Mode != "history_only" {
		t.Errorf("mode = %q, want history_only", report.Mode)
	}
}

func TestFormatReportClaudeAdapter(t *testing.T) {
	r := &Report{
		Adapter: "claude",
		Window:  "last 2h",
		Mode:    "hook_events_plus_history",
		ShouldCompress: ShouldCompressStats{
			Total:            2,
			CorrectlyWrapped: 2,
			CompressRecall:   100.0,
		},
		ShouldPassthrough: ShouldPassthroughStats{
			Total:                1,
			CorrectlyPassthrough: 1,
			PassthroughPrecision: 100.0,
		},
		MissedHighNoise: []string{},
		FalsePositive:   []string{},
		Recommendations: []string{},
		Targets: TargetStats{
			CompressRecallTarget:       "90%",
			PassthroughPrecisionTarget: "98%",
			SummaryFidelityTarget:      "95%",
		},
		Verdict: "pass",
	}
	out := FormatReport(r, false)
	if !strings.Contains(out, "XiT Claude Code Routing Hit Rate") {
		t.Errorf("expected Claude Code header, got: %s", out[:min(len(out), 60)])
	}
	if !strings.Contains(out, "verdict: pass") {
		t.Error("missing verdict pass")
	}
}

func TestFormatReportHistoryOnlyClaude(t *testing.T) {
	r := &Report{
		Adapter: "claude",
		Window:  "last 2h",
		Mode:    "history_only",
	}
	out := FormatReport(r, false)
	if !strings.Contains(out, "warning: Claude Code shell command events unavailable") {
		t.Errorf("expected Claude Code warning, got: %s", out)
	}
}

func TestComputeReportBackwardCompat(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userXiTHome := filepath.Join(tmp, "xit")
	_ = os.MkdirAll(filepath.Join(userXiTHome, "kimi-hooks"), 0755)
	_ = os.MkdirAll(projectHome, 0755)

	now := time.Now().Format(time.RFC3339)
	events := `{"time":"` + now + `","original_command":"xit auto go test -v ./...","action":"observe"}` + "\n"
	_ = os.WriteFile(filepath.Join(userXiTHome, "kimi-hooks", "events.jsonl"), []byte(events), 0644)

	// ComputeReport (Kimi wrapper) still reads from kimi-hooks.
	report, err := ComputeReport(userXiTHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.Adapter != "kimi" {
		t.Errorf("adapter = %q, want kimi", report.Adapter)
	}
	if report.ShouldCompress.CorrectlyWrapped != 1 {
		t.Errorf("correctly_wrapped = %d, want 1", report.ShouldCompress.CorrectlyWrapped)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestClaudeReportNoPanicFree(t *testing.T) {
	tmp := t.TempDir()
	projectHome := filepath.Join(tmp, "project")
	userXiTHome := filepath.Join(tmp, "xit")
	_ = os.MkdirAll(filepath.Join(userXiTHome, "claude-hooks"), 0755)
	_ = os.MkdirAll(projectHome, 0755)

	// Write history with a raw_log containing "panic:" — should NOT affect Claude output.
	rawLogPath := filepath.Join(projectHome, "fake.raw.log")
	_ = os.WriteFile(rawLogPath, []byte("panic: runtime error: index out of range\n"), 0644)
	histPath := filepath.Join(projectHome, "history.jsonl")
	rec := `{"timestamp":"` + time.Now().Format(time.RFC3339) + `","command":"go test","exit_code":2,"raw_bytes":100,"summary_bytes":50,"estimated_reduction":0.5,"duration_ms":10,"filter":"test","confidence":"high","policy":"should_compress","raw_log":"` + rawLogPath + `"}` + "\n"
	_ = os.WriteFile(histPath, []byte(rec), 0644)

	now := time.Now().Format(time.RFC3339)
	events := `{"time":"` + now + `","original_command":"xit auto go test -v ./...","action":"observe"}` + "\n"
	_ = os.WriteFile(filepath.Join(userXiTHome, "claude-hooks", "events.jsonl"), []byte(events), 0644)

	report, err := ComputeReportForAdapter("claude", userXiTHome, projectHome, 2*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fidelity must not be computed for Claude.
	if report.SummaryFidelity.XitAutoRuns != 0 {
		t.Errorf("XitAutoRuns = %d, want 0 (fidelity skipped for claude)", report.SummaryFidelity.XitAutoRuns)
	}
	// Formatted output must not mention panic_free.
	out := FormatReport(report, false)
	if strings.Contains(out, "panic_free") {
		t.Errorf("claude report must not contain panic_free, got:\n%s", out)
	}
	if strings.Contains(out, "summary_fidelity:\n") {
		t.Errorf("claude report must not contain summary_fidelity section, got:\n%s", out)
	}
}

func TestVerdictPass(t *testing.T) {
	r := &Report{
		Mode: "hook_events_plus_history",
		ShouldCompress: ShouldCompressStats{
			Total:            10,
			CorrectlyWrapped: 9,
			CompressRecall:   90.0,
		},
		ShouldPassthrough: ShouldPassthroughStats{
			Total:                10,
			CorrectlyPassthrough: 10,
			PassthroughPrecision: 100.0,
		},
		SummaryFidelity: SummaryFidelityStats{
			XitAutoRuns:   10,
			BasicFidelity: 95.0,
		},
	}
	// Recompute verdict logic inline
	passCompress := r.ShouldCompress.Total == 0 || r.ShouldCompress.CompressRecall >= TargetCompressRecall
	passPassthrough := r.ShouldPassthrough.Total == 0 || r.ShouldPassthrough.PassthroughPrecision >= TargetPassthroughPrecision
	passFidelity := r.SummaryFidelity.XitAutoRuns == 0 || r.SummaryFidelity.BasicFidelity >= TargetSummaryFidelity
	if !(passCompress && passPassthrough && passFidelity) {
		t.Error("expected all targets to pass")
	}
}
