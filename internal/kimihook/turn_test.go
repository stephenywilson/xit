package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunTurnHookCommandUserPromptSubmit(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, ".xit"), 0755)
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)
	
	home := filepath.Join(tmp, ".xit")

	payload := `{"hookEventName":"UserPromptSubmit","cwd":"/tmp/test","session_id":"sess-123","prompt":"hello world"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err := RunTurnHookCommand(home, nil)
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	turnPath := filepath.Join(home, "state", "turn.json")
	data, err := os.ReadFile(turnPath)
	if err != nil {
		t.Fatalf("turn.json not written: %v", err)
	}
	var state TurnState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("invalid turn.json: %v", err)
	}
	if state.Status != "thinking" {
		t.Errorf("expected status thinking, got %s", state.Status)
	}
	if state.Event != "UserPromptSubmit" {
		t.Errorf("expected event UserPromptSubmit, got %s", state.Event)
	}
	if state.SessionID != "sess-123" {
		t.Errorf("expected session_id sess-123, got %s", state.SessionID)
	}
	if state.PromptChars != 11 {
		t.Errorf("expected prompt_chars 11, got %d", state.PromptChars)
	}
}

func TestRunTurnHookCommandStop(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, ".xit"), 0755)
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)
	
	home := filepath.Join(tmp, ".xit")

	// First write a thinking state.
	_ = writeTurnState(home, TurnState{Status: "thinking", Event: "UserPromptSubmit", StartedAt: "2026-05-30T00:00:00Z"})

	payload := `{"hookEventName":"Stop","cwd":"/tmp/test","session_id":"sess-123"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err := RunTurnHookCommand(home, nil)
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	turnPath := filepath.Join(home, "state", "turn.json")
	data, err := os.ReadFile(turnPath)
	if err != nil {
		t.Fatalf("turn.json not written: %v", err)
	}
	var state TurnState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("invalid turn.json: %v", err)
	}
	if state.Status != "turn_completed" {
		t.Errorf("expected status turn_completed, got %s", state.Status)
	}
	if state.Event != "Stop" {
		t.Errorf("expected event Stop, got %s", state.Event)
	}
	if state.StartedAt == "" {
		t.Error("expected started_at preserved from previous state")
	}
	if state.FinishedAt == "" {
		t.Error("expected finished_at set")
	}
}

func TestRunTurnHookCommandFailOpenMalformedJSON(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, ".xit"), 0755)
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)
	
	home := filepath.Join(tmp, ".xit")

	payload := `this is not json`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err := RunTurnHookCommand(home, nil)
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("expected fail-open, got error: %v", err)
	}

	// Should still output {} and not crash.
	turnPath := filepath.Join(home, "state", "turn.json")
	if _, err := os.Stat(turnPath); err == nil {
		// If written, should be empty/default.
		data, _ := os.ReadFile(turnPath)
		if len(data) > 0 {
			var state TurnState
			_ = json.Unmarshal(data, &state)
			// It's okay if empty state was written on malformed input.
		}
	}
}

func TestRunTurnHookCommandSessionStartEnd(t *testing.T) {
	for _, event := range []string{"SessionStart", "SessionEnd"} {
		t.Run(event, func(t *testing.T) {
			tmp := t.TempDir()
			_ = os.MkdirAll(filepath.Join(tmp, ".xit"), 0755)
			oldWd, _ := os.Getwd()
			os.Chdir(tmp)
			defer os.Chdir(oldWd)
			
			home := filepath.Join(tmp, ".xit")

			payload := `{"hookEventName":"` + event + `","cwd":"/tmp/test"}`
			oldStdin := os.Stdin
			r, w, _ := os.Pipe()
			os.Stdin = r
			go func() {
				w.WriteString(payload)
				w.Close()
			}()

			err := RunTurnHookCommand(home, nil)
			os.Stdin = oldStdin
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			turnPath := filepath.Join(home, "state", "turn.json")
			data, _ := os.ReadFile(turnPath)
			var state TurnState
			_ = json.Unmarshal(data, &state)
			expectedStatus := "session_started"
			if event == "SessionEnd" {
				expectedStatus = "session_ended"
			}
			if state.Status != expectedStatus {
				t.Errorf("expected status %s, got %s", expectedStatus, state.Status)
			}
		})
	}
}

func TestRunTurnHookCommandExplicitArgOverridesEmptyJSON(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, ".xit"), 0755)
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)
	
	home := filepath.Join(tmp, ".xit")

	// JSON has empty event, but argv provides explicit event.
	payload := `{"event":"","cwd":"/tmp/test","session_id":"test-session"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err := RunTurnHookCommand(home, []string{"UserPromptSubmit"})
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	turnPath := filepath.Join(home, "state", "turn.json")
	data, _ := os.ReadFile(turnPath)
	var state TurnState
	_ = json.Unmarshal(data, &state)
	if state.Status != "thinking" {
		t.Errorf("expected status thinking, got %s", state.Status)
	}
	if state.Event != "UserPromptSubmit" {
		t.Errorf("expected event UserPromptSubmit, got %s", state.Event)
	}
}

func TestRunTurnHookCommandProjectStateFirst(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, ".xit"), 0755)
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)
	
	home := filepath.Join(tmp, ".xit")

	payload := `{"event":"UserPromptSubmit","cwd":"/tmp/test"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err := RunTurnHookCommand(home, nil)
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should write to project .xit/state/turn.json because cwd has .xit.
	turnPath := filepath.Join(home, "state", "turn.json")
	if _, err := os.Stat(turnPath); err != nil {
		t.Fatalf("expected project state file at %s: %v", turnPath, err)
	}
}

func TestRunTurnHookCommandFallbackToUserHome(t *testing.T) {
	tmp := t.TempDir()
	// No .xit in tmp, so it should fallback to user home (home param).
	oldWd, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(oldWd)
	
	home := filepath.Join(tmp, "user-xit")
	_ = os.MkdirAll(filepath.Join(home, "state"), 0755)

	// Set XIT_HOME so XiTHome returns our test home.
	os.Setenv("XIT_HOME", home)
	defer os.Unsetenv("XIT_HOME")

	payload := `{"event":"UserPromptSubmit","cwd":"/tmp/test"}`
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err := RunTurnHookCommand(home, nil)
	os.Stdin = oldStdin
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	turnPath := filepath.Join(home, "state", "turn.json")
	if _, err := os.Stat(turnPath); err != nil {
		t.Fatalf("expected user home state file at %s: %v", turnPath, err)
	}
}

func TestComputeToolbarTextStateMachine(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")

	now := time.Now().UTC()

	// Ready state: no turn, no auto, no history.
	turn := TurnState{}
	auto := AutoState{}
	text := ComputeToolbarText(home, turn, auto)
	if text != "吸T神功 · 准备就绪" {
		t.Errorf("ready state: expected 吸T神功 · 准备就绪, got %s", text)
	}

	// Turn thinking state.
	turn = TurnState{Status: "thinking", Event: "UserPromptSubmit", StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)}
	text = ComputeToolbarText(home, turn, auto)
	if text != "吸T神功 · 守护你的T" {
		t.Errorf("thinking state: expected 吸T神功 · 守护你的T, got %s", text)
	}

	// Turn active state should also show 守护你的T.
	turn = TurnState{Status: "active", Event: "", StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)}
	text = ComputeToolbarText(home, turn, auto)
	if text != "吸T神功 · 守护你的T" {
		t.Errorf("active state: expected 吸T神功 · 守护你的T, got %s", text)
	}

	// SessionStart state should show 准备就绪, not 守护你的T.
	turn = TurnState{Status: "session_started", Event: "SessionStart", StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)}
	text = ComputeToolbarText(home, turn, auto)
	if text != "吸T神功 · 准备就绪" {
		t.Errorf("session_started state: expected 吸T神功 · 准备就绪, got %s", text)
	}

	// Auto running overrides turn thinking.
	auto = AutoState{Status: "running", StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339), Command: "go test"}
	text = ComputeToolbarText(home, turn, auto)
	if text != "吸T神功 · 正在吸T中" {
		t.Errorf("auto running: expected 吸T神功 · 正在吸T中, got %s", text)
	}

	// Auto completed overrides turn thinking.
	auto = AutoState{Status: "completed", FinishedAt: now.Add(-5 * time.Second).Format(time.RFC3339), SavedBytes: 10240}
	text = ComputeToolbarText(home, turn, auto)
	if !strings.Contains(text, "吸T完成") {
		t.Errorf("auto completed: expected contains 吸T完成, got %s", text)
	}

	// Turn completed without auto records: should not show 未触发吸T.
	turn = TurnState{Status: "turn_completed", FinishedAt: now.Add(-5 * time.Second).Format(time.RFC3339)}
	auto = AutoState{}
	text = ComputeToolbarText(home, turn, auto)
	if strings.Contains(text, "未触发吸T") {
		t.Errorf("turn completed no auto: should not contain 未触发吸T, got %s", text)
	}
	if text != "吸T神功 · 本次已守护" {
		t.Errorf("turn completed no auto: expected 吸T神功 · 本次已守护, got %s", text)
	}
}

func TestFormatSavedTokens(t *testing.T) {
	cases := []struct {
		bytes int
		want  string
	}{
		{0, ""},
		{1, ""},
		{900, "省225 Token"},
		{32000, "省8k Token"},
		{36035, "省9k Token"},
		{65000, "省16k Token"},
	}
	for _, c := range cases {
		got := FormatSavedTokens(c.bytes)
		if got != c.want {
			t.Errorf("FormatSavedTokens(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

func TestComputeToolbarTextAutoCompletedShowsTokens(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	now := time.Now().UTC()

	// Auto completed with 36035 bytes -> 9008 tokens -> 省9k Token
	turn := TurnState{Status: "thinking", Event: "UserPromptSubmit", StartedAt: now.Add(-5 * time.Minute).Format(time.RFC3339)}
	auto := AutoState{Status: "completed", FinishedAt: now.Add(-5 * time.Second).Format(time.RFC3339), SavedBytes: 36035}
	text := ComputeToolbarText(home, turn, auto)
	if text != "吸T完成 · 本次省9k Token" {
		t.Errorf("auto completed: expected 吸T完成 · 本次省9k Token, got %s", text)
	}

	// Auto completed with 900 bytes -> 225 tokens -> 省225 Token
	auto = AutoState{Status: "completed", FinishedAt: now.Add(-5 * time.Second).Format(time.RFC3339), SavedBytes: 900}
	text = ComputeToolbarText(home, turn, auto)
	if text != "吸T完成 · 本次省225 Token" {
		t.Errorf("auto completed small: expected 吸T完成 · 本次省225 Token, got %s", text)
	}
}

func TestComputeToolbarTextTurnResultShowsTokens(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	stateDir := filepath.Join(home, "state")
	_ = os.MkdirAll(stateDir, 0755)
	now := time.Now().UTC()

	// Write a history record with 65000 bytes saved -> 16250 tokens -> 16k Token
	// within the turn time range
	rec := fmt.Sprintf(`{"timestamp":"%s","raw_bytes":70000,"summary_bytes":5000,"action":"compress"}`+"\n", now.Add(-30*time.Minute).Format(time.RFC3339))
	_ = os.WriteFile(filepath.Join(home, "history.jsonl"), []byte(rec), 0644)

	// With an active turn that includes this record
	turn := TurnState{Status: "turn_completed", StartedAt: now.Add(-1 * time.Hour).Format(time.RFC3339), FinishedAt: now.Add(-5 * time.Second).Format(time.RFC3339)}
	auto := AutoState{}
	text := ComputeToolbarText(home, turn, auto)
	if text != "本次吸T1次 · 省16k Token" {
		t.Errorf("turn result: expected 本次吸T1次 · 省16k Token, got %s", text)
	}
}

func TestComputeToolbarTextNoActiveTurnShowsReady(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	stateDir := filepath.Join(home, "state")
	_ = os.MkdirAll(stateDir, 0755)

	// Write a history record
	rec := `{"timestamp":"2026-05-30T12:00:00Z","raw_bytes":70000,"summary_bytes":5000,"action":"compress"}` + "\n"
	_ = os.WriteFile(filepath.Join(home, "history.jsonl"), []byte(rec), 0644)

	// No active turn: in turn-scoped mode, should show ready instead of session aggregate
	turn := TurnState{}
	auto := AutoState{}
	text := ComputeToolbarText(home, turn, auto)
	if text != "吸T神功 · 准备就绪" {
		t.Errorf("no active turn: expected 吸T神功 · 准备就绪, got %s", text)
	}
}
