package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderJSONEstimatedReductionIsNumber(t *testing.T) {
	s := &Summary{
		Command:            "echo hello",
		ExitCode:           0,
		DurationMs:         10,
		RawLogPath:         ".xit/runs/test.raw.log",
		Confidence:         "high",
		EstimatedReduction: 0.875,
		Filter:             "test",
		KeyFacts: map[string]interface{}{
			"key": "value",
		},
		FileList:    []string{"a.go"},
		Suggestions: []string{"do this"},
		BodyLines:   []string{"line1", "line2"},
	}

	rendered := s.Render("json")

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(rendered), &data); err != nil {
		t.Fatalf("json unmarshal failed: %v\nrendered:\n%s", err, rendered)
	}

	val, ok := data["estimated_reduction"]
	if !ok {
		t.Fatal("missing estimated_reduction field")
	}

	_, isFloat := val.(float64)
	_, isInt := val.(int)
	if !isFloat && !isInt {
		t.Errorf("estimated_reduction is not a number, got %T: %v", val, val)
	}
}

func TestRenderHumanNotAffected(t *testing.T) {
	s := &Summary{
		Command:            "echo hello",
		ExitCode:           0,
		DurationMs:         10,
		RawLogPath:         ".xit/runs/test.raw.log",
		Confidence:         "high",
		EstimatedReduction: 0.875,
		Filter:             "test",
		KeyFacts:           map[string]interface{}{"key": "value"},
	}

	rendered := s.Render("human")
	if !strings.Contains(rendered, "estimated_reduction: 88%") {
		t.Errorf("human mode output unexpected:\n%s", rendered)
	}
}

func TestRenderAgentNotAffected(t *testing.T) {
	s := &Summary{
		Command:            "echo hello",
		ExitCode:           0,
		DurationMs:         10,
		RawLogPath:         ".xit/runs/test.raw.log",
		Confidence:         "high",
		EstimatedReduction: 0.875,
		Filter:             "test",
		KeyFacts:           map[string]interface{}{"key": "value"},
	}

	rendered := s.Render("agent")
	if !strings.Contains(rendered, "reduction: 88%") {
		t.Errorf("agent mode output unexpected:\n%s", rendered)
	}
}
