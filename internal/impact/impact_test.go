package impact

import (
	"testing"
)

func TestParseContextTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"149k", 149000},
		{"149K", 149000},
		{"149000", 149000},
		{"2m", 2000000},
		{"0", 0},
		{"", 0},
		{"  50k  ", 50000},
	}
	for _, tt := range tests {
		got := ParseContextTokens(tt.input)
		if got != tt.want {
			t.Errorf("ParseContextTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestComputeReportWithoutContext(t *testing.T) {
	report, err := ComputeReport("/nonexistent", 2*60*60*1000000000, 0)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if report.KimiContextAvailable {
		t.Error("expected KimiContextAvailable false")
	}
	if report.Impact.Verdict != "very_weak" {
		t.Errorf("expected very_weak when no context and no savings, got %s", report.Impact.Verdict)
	}
}

func TestComputeReportWithContext(t *testing.T) {
	report, err := ComputeReport("/nonexistent", 2*60*60*1000000000, 149000)
	if err != nil {
		t.Fatalf("ComputeReport error: %v", err)
	}
	if !report.KimiContextAvailable {
		t.Error("expected KimiContextAvailable true")
	}
	if report.KimiContextTokens != 149000 {
		t.Errorf("kimi_context_tokens = %d, want 149000", report.KimiContextTokens)
	}
}

func TestFormatReport(t *testing.T) {
	r := &Report{
		Window:               "last 2h0m0s",
		Mode:                 "inferred_time_window",
		KimiContextTokens:    149000,
		KimiContextAvailable: true,
		XitSession: XitSession{
			AutoCommands:         1,
			SavedBytes:           32000,
			EstimatedSavedTokens: 8000,
			EstimateMethod:       "saved_bytes / 4",
		},
		Impact: ImpactStats{
			SavedVsContext: 5.4,
			Verdict:        "weak",
		},
		Interpretation: "XiT compressed command output successfully, but current Kimi session spent most tokens outside xit auto.",
		Recommendations: []string{
			"improve routing hit rate",
			"reduce final report verbosity",
		},
	}
	out := FormatReport(r)
	if !contains(out, "XiT Kimi Context Impact") {
		t.Error("missing header")
	}
	if !contains(out, "5.4%") {
		t.Error("missing saved_vs_context")
	}
	if !contains(out, "weak") {
		t.Error("missing verdict")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsImpl(s, substr))
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
