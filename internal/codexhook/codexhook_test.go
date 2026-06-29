package codexhook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stephenywilson/xit/internal/vscodebridge"
)

// TestMain redirects vscodebridge's AppendEvent mirror write (MirrorHome())
// into a throwaway temp dir for this package's whole test run, so the many
// VS-Code-bridge tests below (which set VSCODE_PID) never append a real
// event into the machine's actual ~/.xit/events/vscode-ai-bridge.jsonl as a
// side effect of `go test`.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "xit-codexhook-test-mirror-home")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	os.Setenv("XIT_VSCODE_BRIDGE_HOME", tmp)
	os.Exit(m.Run())
}

// bridgeEventTypes reads the VS Code bridge events file (if any) and
// returns the ordered list of event types — "" if the file doesn't exist.
func bridgeEventTypes(t *testing.T, home string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e vscodebridge.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid bridge event JSON: %v\n%s", err, line)
		}
		types = append(types, e.Event)
	}
	return types
}

func TestInstallCreatesHooksJSON(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")

	res, err := Install(project, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.HooksPath == "" {
		t.Error("expected hooks path")
	}

	cfg, err := ReadHooksConfig(project)
	if err != nil {
		t.Fatalf("read hooks.json failed: %v", err)
	}
	if !HasXiTHook(cfg) {
		t.Error("expected XiT hook installed")
	}
	groups := cfg.Hooks["PreToolUse"]
	if len(groups) != 1 {
		t.Fatalf("expected 1 PreToolUse group, got %d", len(groups))
	}
	if groups[0].Matcher != "^Bash$" {
		t.Errorf("expected matcher ^Bash$, got %s", groups[0].Matcher)
	}
	if len(groups[0].Hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(groups[0].Hooks))
	}
	if groups[0].Hooks[0].Type != "command" {
		t.Errorf("expected type command, got %s", groups[0].Hooks[0].Type)
	}
	if groups[0].Hooks[0].Timeout != 30 {
		t.Errorf("expected timeout 30, got %d", groups[0].Hooks[0].Timeout)
	}

	// All four lifecycle events must be registered and their scripts must exist.
	wantEvents := map[string]bool{"UserPromptSubmit": false, "PreToolUse": false, "PostToolUse": false, "Stop": false}
	for _, ev := range res.Events {
		if _, ok := wantEvents[ev.Event]; !ok {
			t.Errorf("unexpected event %q in install result", ev.Event)
			continue
		}
		wantEvents[ev.Event] = true
		if !ev.Installed {
			t.Errorf("event %s reported not installed", ev.Event)
		}
		if _, err := os.Stat(ev.ScriptPath); err != nil {
			t.Errorf("script for %s not found: %v", ev.Event, err)
		}
	}
	for ev, seen := range wantEvents {
		if !seen {
			t.Errorf("expected event %s in install result", ev)
		}
	}
}

func TestInstallPreservesExistingHooks(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(project, ".codex"), 0755)
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/bin/some-hook"}]}]}}` + "\n"
	_ = os.WriteFile(filepath.Join(project, ".codex", "hooks.json"), []byte(existing), 0644)

	_, err := Install(project, home, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	cfg, err := ReadHooksConfig(project)
	if err != nil {
		t.Fatalf("read hooks.json failed: %v", err)
	}
	if len(cfg.Hooks["PreToolUse"]) != 2 {
		t.Fatalf("expected 2 PreToolUse groups, got %d", len(cfg.Hooks["PreToolUse"]))
	}
	found := false
	for _, g := range cfg.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			if h.Command == "/usr/bin/some-hook" {
				found = true
			}
		}
	}
	if !found {
		t.Error("existing hook was not preserved")
	}
}

func TestUninstallRemovesOnlyXiTHook(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")
	_ = os.MkdirAll(filepath.Join(project, ".codex"), 0755)
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/usr/bin/some-hook"}]},{"matcher":"Bash","hooks":[{"type":"command","command":"` + filepath.Join(home, "hooks", "codex-pretooluse-bash.sh") + `"}]}]}}` + "\n"
	_ = os.WriteFile(filepath.Join(project, ".codex", "hooks.json"), []byte(existing), 0644)

	if err := Uninstall(project); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	cfg, err := ReadHooksConfig(project)
	if err != nil {
		t.Fatalf("read hooks.json failed: %v", err)
	}
	if len(cfg.Hooks["PreToolUse"]) != 1 {
		t.Fatalf("expected 1 group after uninstall, got %d", len(cfg.Hooks["PreToolUse"]))
	}
	if cfg.Hooks["PreToolUse"][0].Hooks[0].Command != "/usr/bin/some-hook" {
		t.Errorf("wrong handler remained: %s", cfg.Hooks["PreToolUse"][0].Hooks[0].Command)
	}
}

func TestRunHookCommandAlreadyWrapped(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	payload := `{"tool_name":"Bash","tool_input":{"command":"xit auto go test -v ./..."},"tool_use_id":"tu-1"}`
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	outBytes, _ := io.ReadAll(outR)
	if len(outBytes) != 0 {
		t.Errorf("expected empty stdout, got %q", string(outBytes))
	}

	// Check event log.
	data, _ := os.ReadFile(filepath.Join(home, "codex-hooks", "events.jsonl"))
	if !bytes.Contains(data, []byte(`"action":"observe"`)) {
		t.Errorf("expected observe event, got %s", string(data))
	}
	if !bytes.Contains(data, []byte(`"original_command":"xit auto go test -v ./..."`)) {
		t.Errorf("expected original command in event, got %s", string(data))
	}
}

func TestRunHookCommandUnwrappedHighNoise(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	payload := `{"tool_name":"Bash","tool_input":{"command":"go test -v ./..."},"tool_use_id":"tu-1"}`
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	outBytes, _ := io.ReadAll(outR)
	if len(outBytes) != 0 {
		t.Errorf("expected empty stdout, got %q", string(outBytes))
	}

	data, _ := os.ReadFile(filepath.Join(home, "codex-hooks", "events.jsonl"))
	if !bytes.Contains(data, []byte(`"recommended_command":"xit auto go test -v ./..."`)) {
		t.Errorf("expected recommended command in event, got %s", string(data))
	}
}

func TestRunHookCommandShortCommand(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	payload := `{"tool_name":"Bash","tool_input":{"command":"git status"},"tool_use_id":"tu-1"}`
	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	outBytes, _ := io.ReadAll(outR)
	if len(outBytes) != 0 {
		t.Errorf("expected empty stdout, got %q", string(outBytes))
	}

	data, _ := os.ReadFile(filepath.Join(home, "codex-hooks", "events.jsonl"))
	if !bytes.Contains(data, []byte(`"action":"observe"`)) {
		t.Errorf("expected observe event, got %s", string(data))
	}
}

func TestRunHookCommandFailOpenMalformed(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)

	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() { os.Stdin = oldStdin; os.Stdout = oldStdout }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(`{not json`)
		w.Close()
	}()

	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	RunHookCommand(home)
	outW.Close()

	outBytes, _ := io.ReadAll(outR)
	if len(outBytes) != 0 {
		t.Errorf("expected empty stdout for malformed input, got %q", string(outBytes))
	}
}

func TestStatsEmpty(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	res, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if res.Events != 0 {
		t.Errorf("expected 0 events, got %d", res.Events)
	}
}

func TestStatsWithEvents(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	_ = os.MkdirAll(filepath.Join(home, "codex-hooks"), 0755)
	recs := `{"time":"2026-05-31T12:00:00Z","action":"observe","original_command":"go test"}` + "\n" +
		`{"time":"2026-05-31T12:01:00Z","action":"passthrough","original_command":"git status"}` + "\n" +
		`{"time":"2026-05-31T12:02:00Z","action":"fail_open"}` + "\n"
	_ = os.WriteFile(filepath.Join(home, "codex-hooks", "events.jsonl"), []byte(recs), 0644)

	res, err := Stats(home)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if res.Events != 3 {
		t.Errorf("expected 3 events, got %d", res.Events)
	}
	if res.Observed != 1 {
		t.Errorf("expected 1 observed, got %d", res.Observed)
	}
	if res.Passthrough != 1 {
		t.Errorf("expected 1 passthrough, got %d", res.Passthrough)
	}
	if res.Errors != 1 {
		t.Errorf("expected 1 error, got %d", res.Errors)
	}
}

func TestStatusInstalled(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")
	_, _ = Install(project, home, false)

	st, err := Status(project, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !st.Installed {
		t.Error("expected installed")
	}
	if st.Mode != "observe+turn_lifecycle" {
		t.Errorf("expected observe+turn_lifecycle mode, got %s", st.Mode)
	}
	// PreToolUse now injects turn identity (and reroutes high-noise commands
	// to `xit auto`) so the turn-lifecycle footer can be accumulated/reported.
	if !st.Reroute {
		t.Error("expected reroute enabled (PreToolUse injects turn identity)")
	}
	if !st.FailOpen {
		t.Error("expected fail_open true")
	}
	if len(st.Events) != 4 {
		t.Errorf("expected 4 lifecycle events in status, got %d", len(st.Events))
	}
}

func TestStatusNotInstalled(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, ".xit")
	project := filepath.Join(tmp, "project")

	st, err := Status(project, home)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if st.Installed {
		t.Error("expected not installed")
	}
}

// ---------------------------------------------------------------------------
// Turn lifecycle: UserPromptSubmit / PreToolUse / PostToolUse / Stop
// ---------------------------------------------------------------------------

func TestResetTurnForPromptNewTurn(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	st, err := ResetTurnForPrompt(home, "s1", "t1", "hello")
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if st.RunCount != 0 || st.SavedTokensTotal != 0 {
		t.Errorf("expected fresh turn count=0 saved=0, got count=%d saved=%d", st.RunCount, st.SavedTokensTotal)
	}
}

func TestResetTurnForPromptIdempotentSameTurn(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 1000)
	// Re-delivery of the SAME turn_id must NOT wipe the accumulated count.
	st, err := ResetTurnForPrompt(home, "s1", "t1", "hello again, duplicate event")
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if st.RunCount != 1 || st.SavedTokensTotal != 1000 {
		t.Errorf("duplicate UserPromptSubmit for same turn_id must not reset, got count=%d saved=%d", st.RunCount, st.SavedTokensTotal)
	}
}

func TestResetTurnForPromptNewTurnIDResets(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 500)
	// A genuinely new turn_id (next user prompt) must reset to 0.
	st, err := ResetTurnForPrompt(home, "s1", "t2", "next prompt")
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if st.RunCount != 0 || st.SavedTokensTotal != 0 {
		t.Errorf("new turn_id must reset to count=0 saved=0, got count=%d saved=%d", st.RunCount, st.SavedTokensTotal)
	}
}

func TestResetTurnForPromptContinuationDoesNotReset(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 1000)
	_, _ = IncrementTurnState(home, "s1", "t1", 9283)
	// Even with a brand-new turn_id, the continuation marker must NOT reset
	// the in-progress turn's accumulated counts.
	st, err := ResetTurnForPrompt(home, "s1", "t-continuation", FooterContinuationMarker+" continue")
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if st.RunCount != 2 || st.SavedTokensTotal != 10283 {
		t.Errorf("continuation must not reset turn, got count=%d saved=%d", st.RunCount, st.SavedTokensTotal)
	}
}

func TestIncrementTurnStateAccumulates(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	st, err := IncrementTurnState(home, "s1", "t1", 1000)
	if err != nil {
		t.Fatalf("increment failed: %v", err)
	}
	if st.RunCount != 1 || st.SavedTokensTotal != 1000 {
		t.Fatalf("after #1: expected count=1 saved=1000, got count=%d saved=%d", st.RunCount, st.SavedTokensTotal)
	}
	st, err = IncrementTurnState(home, "s1", "t1", 9283)
	if err != nil {
		t.Fatalf("increment failed: %v", err)
	}
	if st.RunCount != 2 || st.SavedTokensTotal != 10283 {
		t.Errorf("after #2: expected count=2 saved=10283, got count=%d saved=%d", st.RunCount, st.SavedTokensTotal)
	}
}

func TestIncrementTurnStateConcurrentSafe(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")

	const n = 20
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			_, _ = IncrementTurnState(home, "s1", "t1", 100)
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	st := ReadTurnState(home, "s1", "t1")
	if st == nil {
		t.Fatal("expected turn state to exist")
	}
	if st.RunCount != n {
		t.Errorf("expected run_count=%d after %d concurrent increments (no lost updates), got %d", n, n, st.RunCount)
	}
	if st.SavedTokensTotal != n*100 {
		t.Errorf("expected saved_tokens_total=%d, got %d", n*100, st.SavedTokensTotal)
	}
}

func TestBuildFooterLinesFormat(t *testing.T) {
	cases := []struct {
		saved, count int
		want         string
	}{
		{18532, 2, "本次省 约 18.53k Token · 本轮共吸 2次"},
		{841, 1, "本次省 841 Token · 本轮共吸 1次"},
		{999, 3, "本次省 999 Token · 本轮共吸 3次"},
		{1000, 1, "本次省 约 1.00k Token · 本轮共吸 1次"},
	}
	for _, c := range cases {
		st := &TurnState{RunCount: c.count, SavedTokensTotal: c.saved}
		line1, line2 := BuildFooterLines(st)
		if line1 != "吸T神功 · Codex · 守护你的T" {
			t.Errorf("unexpected line1: %s", line1)
		}
		if line2 != c.want {
			t.Errorf("saved=%d count=%d: got %q want %q", c.saved, c.count, line2, c.want)
		}
	}
}

func TestRewriteCommandForTurnAlreadyWrapped(t *testing.T) {
	rewritten, changed := RewriteCommandForTurn("xit auto go test -v ./...", "s1", "t1", "")
	if !changed {
		t.Fatal("expected a rewrite for an already-wrapped command")
	}
	if !strings.Contains(rewritten, "XIT_CODEX_SESSION_ID='s1'") || !strings.Contains(rewritten, "XIT_CODEX_TURN_ID='t1'") {
		t.Errorf("expected injected session/turn env, got: %s", rewritten)
	}
	if !strings.Contains(rewritten, "xit auto go test -v ./...") {
		t.Errorf("expected original command preserved, got: %s", rewritten)
	}
}

func TestRewriteCommandForTurnAlreadyInjectedNoOp(t *testing.T) {
	cmd := "XIT_ADAPTER=codex XIT_CODEX_SESSION_ID='s1' XIT_CODEX_TURN_ID='t1' xit auto go test -v ./..."
	_, changed := RewriteCommandForTurn(cmd, "s1", "t1", "")
	if changed {
		t.Error("expected no-op when turn env already present (avoid double injection)")
	}
}

func TestRewriteCommandForTurnReroutesHighNoise(t *testing.T) {
	rewritten, changed := RewriteCommandForTurn("go test -v ./...", "s1", "t1", "")
	if !changed {
		t.Fatal("expected reroute for an unwrapped high-noise command")
	}
	if !strings.Contains(rewritten, "xit auto go test -v ./...") {
		t.Errorf("expected rerouted through xit auto, got: %s", rewritten)
	}
	if !strings.Contains(rewritten, "XIT_CODEX_TURN_ID='t1'") {
		t.Errorf("expected injected turn env, got: %s", rewritten)
	}
}

func TestRewriteCommandForTurnReroutesLargeShellOutputLoop(t *testing.T) {
	cmd := `bash -lc 'for i in {1..1200}; do echo "vscode-codex-chat-test line=$i aaaa bbbb cccc"; done'`
	rewritten, changed := RewriteCommandForTurn(cmd, "s1", "t1", "")
	if !changed {
		t.Fatal("expected reroute for large shell output loop")
	}
	if !strings.Contains(rewritten, "xit auto bash -lc") {
		t.Fatalf("expected whole shell command to be routed through xit auto, got: %s", rewritten)
	}
	if strings.Contains(rewritten, "xit auto for i in") {
		t.Fatalf("must not route the shell loop body as a direct executable, got: %s", rewritten)
	}
}

func TestRewriteCommandForTurnDoesNotRerouteSmallSilentLoop(t *testing.T) {
	cmd := `for f in a b c; do test -f "$f"; done`
	rewritten, changed := RewriteCommandForTurn(cmd, "s1", "t1", "")
	if changed {
		t.Fatalf("expected small non-output loop to remain unchanged, got: %s", rewritten)
	}
	if rewritten != cmd {
		t.Fatalf("expected original command, got: %s", rewritten)
	}
}

func TestRewriteCommandForTurnReroutesSeqPipe(t *testing.T) {
	cmd := `bash -lc 'seq 1 1200 | sed "s/^/line=/"'`
	rewritten, changed := RewriteCommandForTurn(cmd, "s1", "t1", "")
	if !changed {
		t.Fatal("expected seq pipe to be rerouted")
	}
	if !strings.Contains(rewritten, "xit auto bash -lc") {
		t.Fatalf("expected whole shell command reroute, got: %s", rewritten)
	}
}

// TestRewriteCommandForTurnNeverReroutesPipedCommand guards against a real
// semantic-breaking bug: `xit auto` execs its argv directly (no shell), so
// rerouting a piped command (e.g. "find ... | xargs wc -l") through it would
// only wrap the producer stage and pipe XiT's short compressed summary into
// the consumer stage instead of the real data. Must be left untouched.
func TestRewriteCommandForTurnNeverReroutesPipedCommand(t *testing.T) {
	cmd := `find . -name "*.go" 2>/dev/null | xargs wc -l`
	rewritten, changed := RewriteCommandForTurn(cmd, "s1", "t1", "")
	if changed {
		t.Errorf("expected no rewrite for a piped command (would break the pipeline), got: %s", rewritten)
	}
	if rewritten != cmd {
		t.Errorf("expected command unchanged, got: %s", rewritten)
	}
}

// TestRewriteCommandForTurnAlreadyWrappedPipeIsAIChoicePreserved verifies that
// when the AI itself already wrote "xit auto X | Y" (a deliberate pipe of
// XiT's own output), the env prefix is still injected before "xit auto" and
// the pipe is preserved as-is — this is NOT the reroute-breaks-semantics case
// since the AI chose the pipe deliberately, not XiT.
func TestRewriteCommandForTurnAlreadyWrappedPipeIsAIChoicePreserved(t *testing.T) {
	cmd := `xit auto go test -v ./... | tee /tmp/out.log`
	rewritten, changed := RewriteCommandForTurn(cmd, "s1", "t1", "")
	if !changed {
		t.Fatal("expected env injection for an already-wrapped command even with a trailing pipe")
	}
	if !strings.Contains(rewritten, "XIT_CODEX_TURN_ID='t1'") {
		t.Errorf("expected injected turn env, got: %s", rewritten)
	}
	if !strings.HasSuffix(rewritten, "| tee /tmp/out.log") {
		t.Errorf("expected the AI's own trailing pipe preserved verbatim, got: %s", rewritten)
	}
}

func TestRewriteCommandForTurnShortCommandNoOp(t *testing.T) {
	_, changed := RewriteCommandForTurn("git status", "s1", "t1", "")
	if changed {
		t.Error("expected no rewrite for a short/passthrough command")
	}
}

func TestRewriteCommandForTurnShellWrapper(t *testing.T) {
	rewritten, changed := RewriteCommandForTurn(`bash -lc 'xit auto go test -v ./...'`, "s1", "t1", "")
	if !changed {
		t.Fatal("expected a rewrite for a shell-wrapped already-wrapped command")
	}
	if !strings.Contains(rewritten, "XIT_CODEX_TURN_ID='t1'") {
		t.Errorf("expected injected turn env inside the shell wrapper, got: %s", rewritten)
	}
	if !strings.HasPrefix(rewritten, "bash -lc ") {
		t.Errorf("expected the bash -lc wrapper preserved, got: %s", rewritten)
	}
}

// TestResolveCodexHomeXITHomeWinsOverPayloadCwd is a regression test for a
// real confirmed bug: `xit auto` resolves its home via xitHome(), which
// checks XIT_HOME unconditionally before ever considering cwd. If
// resolveCodexHome preferred the hook payload's cwd over an explicitly-set
// XIT_HOME, the four lifecycle hooks would silently read/write a DIFFERENT
// turn-state file than the one `xit auto` itself accumulates into — exactly
// reproduced as: PostToolUse/Stop returning {} (turn "not found") even though
// `xit auto` had genuinely run twice and accumulated real savings. XIT_HOME
// must win whenever it is set, regardless of payload cwd.
func TestResolveCodexHomeXITHomeWinsOverPayloadCwd(t *testing.T) {
	xitHomeDir := filepath.Join(t.TempDir(), "xit-home")
	otherCwd := filepath.Join(t.TempDir(), "other-project")
	t.Setenv("XIT_HOME", xitHomeDir)

	got := resolveCodexHome("fallback-should-not-matter", otherCwd)
	if got != xitHomeDir {
		t.Fatalf("expected XIT_HOME (%q) to win over payload cwd (%q), got: %q", xitHomeDir, otherCwd, got)
	}
}

// TestResolveCodexHomeFallsBackToPayloadCwdWhenXITHomeUnset verifies the
// payload's cwd is still used (more accurate than the hook process's own
// os.Getwd()) when XIT_HOME is NOT set — the common real-Codex case.
func TestResolveCodexHomeFallsBackToPayloadCwdWhenXITHomeUnset(t *testing.T) {
	t.Setenv("XIT_HOME", "")
	os.Unsetenv("XIT_HOME")
	cwd := filepath.Join(t.TempDir(), "project")

	got := resolveCodexHome("fallback", cwd)
	want := filepath.Join(cwd, ".xit")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// runHandler pipes payload into stdin, captures stdout, and runs fn(home).
func runHandler(t *testing.T, home, payload string, fn func(home string) error) string {
	t.Helper()
	oldStdin, oldStdout := os.Stdin, os.Stdout
	defer func() { os.Stdin, os.Stdout = oldStdin, oldStdout }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()
	outR, outW, _ := os.Pipe()
	os.Stdout = outW

	if err := fn(home); err != nil {
		t.Fatalf("handler failed: %v", err)
	}
	outW.Close()
	out, _ := io.ReadAll(outR)
	return string(out)
}

func parseHookJSON(t *testing.T, out string) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, out)
	}
	return resp
}

func TestHandleUserPromptSubmitStartsTurn(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	out := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","prompt":"hello","hook_event_name":"UserPromptSubmit"}`, HandleUserPromptSubmit)
	if out != "" {
		t.Errorf("expected empty stdout, got: %q", out)
	}
	st := ReadTurnState(home, "s1", "t1")
	if st == nil || st.TurnID != "t1" || st.RunCount != 0 {
		t.Errorf("expected fresh turn t1 count=0, got: %+v", st)
	}
}

func TestCodexUserPromptSubmitNoopHasEmptyStdout(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	out := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","prompt":"hello","hook_event_name":"UserPromptSubmit"}`, HandleUserPromptSubmit)
	if out != "" {
		t.Fatalf("expected empty stdout, got %q", out)
	}
}

// TestHandleUserPromptSubmitStartsVSCodeBridgeTurn: a genuinely new
// user-initiated prompt, inside VS Code, must emit turn.started — the
// signal that drives the Dashboard/status bar "正在守护" (thinking) state.
func TestHandleUserPromptSubmitStartsVSCodeBridgeTurn(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	runHandler(t, home, `{"session_id":"s1","turn_id":"t1","prompt":"hello","hook_event_name":"UserPromptSubmit"}`, HandleUserPromptSubmit)
	types := bridgeEventTypes(t, home)
	if len(types) != 1 || types[0] != "turn.started" {
		t.Fatalf("expected exactly one turn.started bridge event, got: %v", types)
	}
}

func TestHandleUserPromptSubmitDoesNotStartBridgeTurnOutsideVSCode(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "")
	runHandler(t, home, `{"session_id":"s1","turn_id":"t1","prompt":"hello","hook_event_name":"UserPromptSubmit"}`, HandleUserPromptSubmit)
	if types := bridgeEventTypes(t, home); types != nil {
		t.Fatalf("expected no bridge event outside VS Code, got: %v", types)
	}
}

// TestHandleUserPromptSubmitFooterContinuationDoesNotStartNewBridgeTurn: our
// OWN footer-continuation re-submission (see FooterContinuationMarker) is
// not a real new user turn — it must never flash "正在守护" right as the
// final result is about to be shown.
func TestHandleUserPromptSubmitFooterContinuationDoesNotStartNewBridgeTurn(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	payload := `{"session_id":"s1","turn_id":"t1","prompt":"` + FooterContinuationMarker + ` continue","hook_event_name":"UserPromptSubmit"}`
	runHandler(t, home, payload, HandleUserPromptSubmit)
	if types := bridgeEventTypes(t, home); types != nil {
		t.Fatalf("expected no turn.started for our own footer-continuation resubmission, got: %v", types)
	}
}

func TestHandlePreToolUseInjectsEnvAndUpdatesInput(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_use_id":"tu1","tool_input":{"command":"xit auto go test -v ./..."}}`
	out := runHandler(t, home, payload, HandlePreToolUse)
	resp := parseHookJSON(t, out)
	hso, ok := resp["hookSpecificOutput"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected hookSpecificOutput in response, got: %s", out)
	}
	if hso["hookEventName"] != "PreToolUse" {
		t.Fatalf("expected hookEventName=PreToolUse, got: %v", hso["hookEventName"])
	}
	if hso["permissionDecision"] != "allow" {
		t.Fatalf("expected permissionDecision=allow, got: %v", hso["permissionDecision"])
	}
	updated, ok := hso["updatedInput"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected updatedInput under hookSpecificOutput, got: %s", out)
	}
	cmd, _ := updated["command"].(string)
	if !strings.Contains(cmd, "XIT_CODEX_TURN_ID='t1'") {
		t.Errorf("expected injected turn env in updated command, got: %q", cmd)
	}
}

func TestHandlePreToolUseWritesVSCodeBridgeStartedEvent(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "project", ".xit")
	workspace := filepath.Join(tmp, "project")
	t.Setenv("VSCODE_PID", "4242")

	payload := `{"session_id":"s1","turn_id":"t1","cwd":"` + workspace + `","tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`
	out := runHandler(t, home, payload, HandlePreToolUse)
	resp := parseHookJSON(t, out)
	if resp["hookSpecificOutput"] == nil {
		t.Fatalf("expected command rewrite, got: %s", out)
	}
	rewritten, _ := resp["hookSpecificOutput"].(map[string]interface{})["updatedInput"].(map[string]interface{})["command"].(string)
	if !strings.Contains(rewritten, "XIT_VSCODE_BRIDGE_RUN_ID=") {
		t.Fatalf("expected bridge run id injected into the rewritten command, got: %s", rewritten)
	}

	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatal(err)
	}
	if event["schema"] != "xit.vscode-ai-bridge.v1" || event["event"] != "run.started" ||
		event["host"] != "vscode" || event["surface"] != "codex_chat" || event["adapter"] != "codex" {
		t.Fatalf("bad bridge started event: %#v", event)
	}
	runID, _ := event["run_id"].(string)
	if runID == "" || !strings.Contains(rewritten, runID) {
		t.Fatalf("expected the event's run_id to match the id injected into the rewritten command: run_id=%q rewritten=%q", runID, rewritten)
	}
	if strings.Contains(string(data), "go test") || strings.Contains(string(data), workspace) || strings.Contains(string(data), "s1") {
		t.Fatalf("bridge event leaked sensitive raw values: %s", string(data))
	}
}

// TestHandlePreToolUseDoesNotInjectBridgeRunIDOutsideVSCode confirms ordinary
// Codex CLI usage (no VSCODE_PID at all) never gets a useless
// XIT_VSCODE_BRIDGE_RUN_ID env var in its visible rewritten command.
func TestHandlePreToolUseDoesNotInjectBridgeRunIDOutsideVSCode(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "")

	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"go test -v ./..."}}`
	out := runHandler(t, home, payload, HandlePreToolUse)
	resp := parseHookJSON(t, out)
	rewritten, _ := resp["hookSpecificOutput"].(map[string]interface{})["updatedInput"].(map[string]interface{})["command"].(string)
	if strings.Contains(rewritten, "XIT_VSCODE_BRIDGE_RUN_ID=") {
		t.Fatalf("expected no bridge run id outside VS Code, got: %s", rewritten)
	}
	if _, err := os.Stat(filepath.Join(home, "events", "vscode-ai-bridge.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("bridge file should not exist for ordinary Codex CLI, err=%v", err)
	}
}

func TestCodexPreToolUseOutputValidJSON(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	payload := `{"session_id":"session-test-1","turn_id":"turn-test-1","tool_name":"Bash","tool_input":{"command":"xit auto bash -lc 'for i in {1..20}; do echo line-$i; done'"}}`
	_ = parseHookJSON(t, runHandler(t, home, payload, HandlePreToolUse))
}

func TestCodexPreToolUseRewriteShape(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	payload := `{"session_id":"session-test-1","turn_id":"turn-test-1","tool_name":"Bash","tool_input":{"command":"xit auto bash -lc 'for i in {1..20}; do echo line-$i; done'"}}`
	resp := parseHookJSON(t, runHandler(t, home, payload, HandlePreToolUse))
	hso := resp["hookSpecificOutput"].(map[string]interface{})
	if hso["hookEventName"] != "PreToolUse" || hso["permissionDecision"] != "allow" {
		t.Fatalf("wrong PreToolUse hookSpecificOutput shape: %#v", hso)
	}
	updated, ok := hso["updatedInput"].(map[string]interface{})
	if !ok {
		t.Fatalf("updatedInput missing or wrong type: %#v", hso["updatedInput"])
	}
	cmd, ok := updated["command"].(string)
	if !ok {
		t.Fatalf("updatedInput.command must be string: %#v", updated["command"])
	}
	for _, want := range []string{"XIT_ADAPTER=codex", "XIT_CODEX_SESSION_ID='session-test-1'", "XIT_CODEX_TURN_ID='turn-test-1'", "xit auto"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("rewritten command missing %q: %s", want, cmd)
		}
	}
}

func TestCodexPreToolUseNoopHasEmptyStdout(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"git status --short"}}`
	out := runHandler(t, home, payload, HandlePreToolUse)
	if out != "" {
		t.Fatalf("expected empty stdout for noop, got %q", out)
	}
}

func TestHandlePostToolUseEmptyForXitToolCall(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)

	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_use_id":"tu1","tool_input":{"command":"XIT_ADAPTER=codex xit auto go test -v ./..."}}`
	out := runHandler(t, home, payload, HandlePostToolUse)
	if out != "" {
		t.Fatalf("expected PostToolUse stdout to be empty for xit auto calls, got %q", out)
	}
}

func TestCodexPostToolUseDoesNotReturnAdditionalContext(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"XIT_ADAPTER=codex XIT_CODEX_SESSION_ID='s1' XIT_CODEX_TURN_ID='t1' xit auto go test -v ./..."}}`
	out := runHandler(t, home, payload, HandlePostToolUse)
	if out != "" {
		t.Fatalf("expected no additionalContext / empty stdout, got %q", out)
	}
}

func TestHandlePostToolUseNoContextForUnrelatedCommand(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)

	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_use_id":"tu2","tool_input":{"command":"git status"}}`
	out := runHandler(t, home, payload, HandlePostToolUse)
	if out != "" {
		t.Errorf("expected empty stdout for an unrelated tool call, got: %q", out)
	}
}

func TestCodexPostToolUseNoopHasEmptyStdout(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	payload := `{"session_id":"s1","turn_id":"t1","tool_name":"Bash","tool_input":{"command":"git status"}}`
	out := runHandler(t, home, payload, HandlePostToolUse)
	if out != "" {
		t.Fatalf("expected empty stdout, got %q", out)
	}
}

func TestHandleStopNoFooterNeededWhenNoRuns(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"Sure, done."}`
	out := runHandler(t, home, payload, HandleStop)
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {} when run_count==0, got: %q", out)
	}
}

func TestCodexStopAllowShape(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	out := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"done"}`, HandleStop)
	resp := parseHookJSON(t, out)
	if len(resp) != 0 {
		t.Fatalf("expected empty JSON object for allow, got %#v", resp)
	}
}

func TestHandleStopAllowsWhenFooterAlreadyPresent(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)

	lastMsg := "All done.\\n\\n吸T神功 · Codex · 守护你的T\\n本次省 约 18.53k Token · 本轮共吸 1次"
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"` + lastMsg + `"}`
	out := runHandler(t, home, payload, HandleStop)
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {} (allow) when footer already present, got: %q", out)
	}
	if ReadTurnState(home, "s1", "t1") != nil {
		t.Error("expected turn state cleaned up after footer confirmed")
	}
}

func TestHandleStopBlocksOnceWhenFooterMissing(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)

	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"All done, no footer here."}`
	out := runHandler(t, home, payload, HandleStop)
	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if resp["decision"] != "block" {
		t.Fatalf("expected decision=block on first missing-footer Stop, got: %s", out)
	}
	reason, _ := resp["reason"].(string)
	if !strings.Contains(reason, FooterContinuationMarker) {
		t.Errorf("expected continuation marker in reason, got: %q", reason)
	}
	if !strings.Contains(reason, "吸T神功 · Codex · 守护你的T") || !strings.Contains(reason, "本轮共吸 1次") {
		t.Errorf("expected exact footer text in reason, got: %q", reason)
	}
}

// ──────────────────────────────────────────────────────────────────
// turn.finished VS Code Bridge wiring: must fire on every path that
// genuinely ENDS the turn (no real activity, footer confirmed, or
// loop-prevention fail-open) — but never on the "block, continue" path,
// where Codex hasn't actually finished its final answer yet.
// ──────────────────────────────────────────────────────────────────

func TestHandleStopEmitsTurnFinishedWhenNoRealActivity(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"Sure, done."}`
	runHandler(t, home, payload, HandleStop)
	types := bridgeEventTypes(t, home)
	if len(types) != 1 || types[0] != "turn.finished" {
		t.Fatalf("expected exactly one turn.finished bridge event (no real xit auto activity this turn), got: %v", types)
	}
}

func TestHandleStopEmitsTurnFinishedWhenFooterConfirmed(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	lastMsg := "All done.\\n\\n吸T神功 · Codex · 守护你的T\\n本次省 约 18.53k Token · 本轮共吸 1次"
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"` + lastMsg + `"}`
	runHandler(t, home, payload, HandleStop)
	types := bridgeEventTypes(t, home)
	if len(types) != 1 || types[0] != "turn.finished" {
		t.Fatalf("expected exactly one turn.finished bridge event (footer confirmed = real final answer done), got: %v", types)
	}
}

func TestHandleStopDoesNotEmitTurnFinishedOnFirstBlock(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"All done, no footer here."}`
	runHandler(t, home, payload, HandleStop)
	// The turn is NOT over yet — Codex is being asked to append the footer
	// and will call Stop again. Promoting "收工中" to a final result here
	// would be premature.
	if types := bridgeEventTypes(t, home); types != nil {
		t.Fatalf("expected no turn.finished on the first block-and-continue Stop call, got: %v", types)
	}
}

func TestHandleStopEmitsTurnFinishedOnFailOpenExhausted(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)

	// First Stop: blocks once (footer missing).
	payload1 := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"All done, no footer."}`
	runHandler(t, home, payload1, HandleStop)
	if types := bridgeEventTypes(t, home); types != nil {
		t.Fatalf("expected no turn.finished yet after the first block, got: %v", types)
	}

	// Second Stop: stop_hook_active=true (Codex's continuation retry) and the
	// footer STILL never showed up — loop-prevention fail-open. The turn is
	// genuinely over now (XiT gives up), so turn.finished must fire so
	// "收工中" doesn't get stuck forever.
	payload2 := `{"session_id":"s1","turn_id":"t1","stop_hook_active":true,"last_assistant_message":"Still no footer."}`
	runHandler(t, home, payload2, HandleStop)
	types := bridgeEventTypes(t, home)
	if len(types) != 1 || types[0] != "turn.finished" {
		t.Fatalf("expected exactly one turn.finished bridge event after fail-open exhausted, got: %v", types)
	}
}

func TestHandleStopDoesNotEmitTurnFinishedOutsideVSCode(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	lastMsg := "All done.\\n\\n吸T神功 · Codex · 守护你的T\\n本次省 约 18.53k Token · 本轮共吸 1次"
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"` + lastMsg + `"}`
	runHandler(t, home, payload, HandleStop)
	if types := bridgeEventTypes(t, home); types != nil {
		t.Fatalf("expected no bridge event for ordinary Codex CLI usage (no VSCODE_PID), got: %v", types)
	}
}

// TestHandleStopReasonDoesNotLeakVisibleInternalMarker guards against a real
// Codex screenshot bug: Codex echoes the Stop "block" reason text back into
// the chat transcript while the continuation is in flight, so anything
// visible there is visible to the user. The bracketed continuation marker
// and the verbose internal instruction text must never appear as literal,
// human-readable content.
func TestHandleStopReasonDoesNotLeakVisibleInternalMarker(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 20920)

	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"All done, no footer here."}`
	out := runHandler(t, home, payload, HandleStop)
	resp := parseHookJSON(t, out)
	reason, _ := resp["reason"].(string)

	if strings.Contains(reason, "[XIT_CODEX_FOOTER_CONTINUATION]") {
		t.Errorf("reason must not contain the literal visible marker text, got: %q", reason)
	}
	for _, leaked := range []string{
		"不要解释 XiT",
		"保持上一条最终回答正文完全不变",
		"请在上一条回答末尾追加以下两行",
	} {
		if strings.Contains(reason, leaked) {
			t.Errorf("reason must not leak internal instruction phrase %q, got: %q", leaked, reason)
		}
	}
	if !strings.Contains(reason, "XiT 已追加本轮 Token 节省摘要") {
		t.Errorf("expected the short, user-acceptable passive note, got: %q", reason)
	}
	// The marker constant itself must still be present (functionally required
	// so UserPromptSubmit can recognize the continuation) but it must be
	// built entirely from zero-width characters, i.e. invisible when rendered.
	if !strings.Contains(reason, FooterContinuationMarker) {
		t.Fatal("reason must still carry FooterContinuationMarker for turn-continuation detection")
	}
	for _, r := range FooterContinuationMarker {
		if r > 0x2100 {
			t.Fatalf("FooterContinuationMarker must only use zero-width characters, found visible rune %U", r)
		}
	}
}

// TestHandleStopFooterTextUnchanged locks the exact, already-accepted footer
// shape so the marker/wording cleanup above can never regress it.
func TestHandleStopFooterTextUnchanged(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 20920)

	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"done"}`
	out := runHandler(t, home, payload, HandleStop)
	resp := parseHookJSON(t, out)
	reason, _ := resp["reason"].(string)

	if !strings.Contains(reason, "吸T神功 · Codex · 守护你的T") {
		t.Errorf("footer line1 missing/changed, got: %q", reason)
	}
	if !strings.Contains(reason, "本次省 约 20.92k Token · 本轮共吸 1次") {
		t.Errorf("footer line2 missing/changed, got: %q", reason)
	}
}

func TestCodexStopContinuationShape(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	out := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"done"}`, HandleStop)
	resp := parseHookJSON(t, out)
	if resp["decision"] != "block" {
		t.Fatalf("expected decision=block, got %#v", resp)
	}
	reason, ok := resp["reason"].(string)
	if !ok || !strings.Contains(reason, FooterContinuationMarker) {
		t.Fatalf("expected continuation marker in reason, got %#v", resp["reason"])
	}
}

func TestCodexStopBlocksWithTwoRunFooterWithoutPostToolUse(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 4250)
	_, _ = IncrementTurnState(home, "s1", "t1", 5710)

	out := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"done"}`, HandleStop)
	resp := parseHookJSON(t, out)
	if resp["decision"] != "block" {
		t.Fatalf("expected first Stop to block, got %#v", resp)
	}
	reason, _ := resp["reason"].(string)
	if !strings.Contains(reason, "本次省 约 9.96k Token") || !strings.Contains(reason, "本轮共吸 2次") {
		t.Fatalf("expected Stop reason to use real two-run totals, got %q", reason)
	}
	if !strings.Contains(reason, FooterContinuationMarker) {
		t.Fatalf("expected continuation marker in reason, got %q", reason)
	}
}

func TestHandleStopNeverBlocksTwice(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)

	payload1 := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"All done, no footer."}`
	out1 := runHandler(t, home, payload1, HandleStop)
	var resp1 map[string]interface{}
	_ = json.Unmarshal([]byte(out1), &resp1)
	if resp1["decision"] != "block" {
		t.Fatalf("expected first Stop to block, got: %s", out1)
	}

	// Second Stop call: Codex sets stop_hook_active=true on the continuation
	// retry. Even if the footer STILL never showed up, must NOT block again.
	payload2 := `{"session_id":"s1","turn_id":"t1","stop_hook_active":true,"last_assistant_message":"Still no footer."}`
	out2 := runHandler(t, home, payload2, HandleStop)
	if strings.TrimSpace(out2) != "{}" {
		t.Errorf("expected {} (no second block — loop prevention), got: %q", out2)
	}
	if ReadTurnState(home, "s1", "t1") != nil {
		t.Error("expected turn state cleaned up after fail-open (avoid leaking state)")
	}
}

func TestCodexStopNoLoop(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	out1 := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"done"}`, HandleStop)
	if parseHookJSON(t, out1)["decision"] != "block" {
		t.Fatalf("expected first Stop to block, got %s", out1)
	}
	out2 := runHandler(t, home, `{"session_id":"s1","turn_id":"t1","stop_hook_active":true,"last_assistant_message":"done"}`, HandleStop)
	resp2 := parseHookJSON(t, out2)
	if len(resp2) != 0 {
		t.Fatalf("expected second Stop to allow with {}, got %#v", resp2)
	}
}

func TestCodexNewTurnResets(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "turn-test-1", "hello")
	_, _ = IncrementTurnState(home, "s1", "turn-test-1", 123)
	_, _ = ResetTurnForPrompt(home, "s1", "turn-test-2", "hello again")
	st := ReadTurnState(home, "s1", "turn-test-2")
	if st == nil || st.TurnID != "turn-test-2" || st.RunCount != 0 || st.SavedTokensTotal != 0 {
		t.Fatalf("expected new turn to start at zero, got %+v", st)
	}
}

func TestCodexConcurrentTurnUpdate(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = IncrementTurnState(home, "s1", "t1", 7)
		}()
	}
	wg.Wait()
	st := ReadTurnState(home, "s1", "t1")
	if st == nil || st.RunCount != 20 || st.SavedTokensTotal != 140 {
		t.Fatalf("expected 20 atomic updates, got %+v", st)
	}
}

func TestHandleStopFooterContinuationUsedPreventsReBlockEvenWithoutActiveFlag(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "s1", "t1", "hello")
	_, _ = IncrementTurnState(home, "s1", "t1", 18532)
	_ = MarkFooterContinuationUsed(home, "s1", "t1")

	// Even if Codex (unexpectedly) sends stop_hook_active=false again, the
	// persisted footer_continuation_used flag must prevent a second block.
	payload := `{"session_id":"s1","turn_id":"t1","stop_hook_active":false,"last_assistant_message":"Still nothing."}`
	out := runHandler(t, home, payload, HandleStop)
	if strings.TrimSpace(out) != "{}" {
		t.Errorf("expected {} (footer_continuation_used already true -> no re-block), got: %q", out)
	}
}

func TestCrossAdapterIsolationCodexTurnStateIndependent(t *testing.T) {
	// Codex turn state lives under state/codex-turns/<session>/<turn>.json in the SAME
	// XIT_HOME that Agy/Claude use for current-run.json — verify they cannot
	// collide or be read by each other's code path.
	home := filepath.Join(t.TempDir(), ".xit")
	_, _ = ResetTurnForPrompt(home, "codex-session", "t1", "hello")
	_, _ = IncrementTurnState(home, "codex-session", "t1", 18532)

	// Simulate an Agy/Claude current-run.json existing alongside it.
	_ = os.MkdirAll(filepath.Join(home, "state"), 0755)
	_ = os.WriteFile(filepath.Join(home, "state", "current-run.json"),
		[]byte(`{"status":"completed","adapter":"antigravity","saved_bytes":293640}`), 0644)

	st := ReadTurnState(home, "codex-session", "t1")
	if st == nil || st.SavedTokensTotal != 18532 {
		t.Fatalf("expected codex turn state intact despite sibling current-run.json, got: %+v", st)
	}
	// And a different session_id must never see this turn's data.
	if ReadTurnState(home, "some-other-session", "t1") != nil {
		t.Error("expected nil for an unrelated session_id (no cross-session leak)")
	}
}
