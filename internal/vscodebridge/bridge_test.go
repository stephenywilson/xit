package vscodebridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain redirects MirrorHome() (the AppendEvent dual-write target — see
// bridge.go) into a throwaway temp dir for the whole package's test run.
// Without this, every test below that sets VSCODE_PID would also append a
// real event into the machine's actual ~/.xit/events/vscode-ai-bridge.jsonl
// as a side effect of just running `go test`.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "xit-vscodebridge-test-mirror-home")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	os.Setenv("XIT_VSCODE_BRIDGE_HOME", tmp)
	os.Exit(m.Run())
}

func TestCodexVSCodeSourceRecognition(t *testing.T) {
	if !IsCodexVSCode(Env{VSCodePID: "123"}) {
		t.Fatal("expected VSCODE_PID present to be recognized")
	}
	if IsCodexVSCode(Env{VSCodePID: ""}) {
		t.Fatal("missing VSCODE_PID (ordinary terminal Codex CLI outside VS Code) must not be recognized")
	}
}

// TestClaudeVSCodeSourceRecognition: IsVSCodeHost is the adapter-neutral
// name for the exact same VSCODE_PID check IsCodexVSCode performs — Claude
// Code's hook has no Codex-equivalent originator/thread env signal at all,
// so VSCODE_PID is the only ambient host signal available to it too.
func TestClaudeVSCodeSourceRecognition(t *testing.T) {
	if !IsVSCodeHost(Env{VSCodePID: "123"}) {
		t.Fatal("expected VSCODE_PID present to be recognized")
	}
	if IsVSCodeHost(Env{VSCodePID: ""}) {
		t.Fatal("missing VSCODE_PID (ordinary terminal Claude CLI outside VS Code) must not be recognized")
	}
}

func TestOrdinaryClaudeCLIDoesNotGenerateBridge(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	// Ordinary Claude CLI run in a plain terminal, outside VS Code entirely:
	// VSCODE_PID is genuinely unset in that environment.
	t.Setenv("VSCODE_PID", "")
	if _, ok := StartClaudeIfVSCode(home, "/tmp/ws", "xit auto go test -v ./...", time.Now()); ok {
		t.Fatal("ordinary Claude CLI (no VSCODE_PID) must not start a bridge run")
	}
	if _, err := os.Stat(filepath.Join(home, "events", "vscode-ai-bridge.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("bridge file should not exist, err=%v", err)
	}
}

func TestClaudeStartedAndFinishedEvents(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	startedAt := time.Now().UTC()
	ctx, ok := StartClaudeIfVSCode(home, workspace, "xit auto find . -maxdepth 4 -type f", startedAt)
	if !ok || ctx == nil {
		t.Fatal("expected started bridge event")
	}

	if !FinishClaudeIfPending(home, workspace, FinishResult{
		ExitCode:     0,
		SavedTokens:  22720,
		SavedBytes:   90880,
		SummaryBytes: 3800,
		RunCount:     1,
	}, startedAt.Add(time.Second)) {
		t.Fatal("expected finished bridge event")
	}

	events := readEvents(t, home)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Event != "run.started" || events[0].Host != Host ||
		events[0].Surface != SurfaceClaudeCode || events[0].Adapter != AdapterClaude {
		t.Fatalf("bad started event: %+v", events[0])
	}
	if events[0].RunID != ctx.RunID || events[1].RunID != ctx.RunID {
		t.Fatalf("started/finished run_id mismatch: started=%s finished=%s want=%s", events[0].RunID, events[1].RunID, ctx.RunID)
	}
	fin := events[1]
	if fin.Event != "run.finished" || fin.Surface != SurfaceClaudeCode || fin.Adapter != AdapterClaude {
		t.Fatalf("bad finished event: %+v", fin)
	}
	if fin.ExitCode == nil || *fin.ExitCode != 0 ||
		fin.SavedTokens == nil || *fin.SavedTokens != 22720 ||
		fin.SavedBytes == nil || *fin.SavedBytes != 90880 ||
		fin.SummaryBytes == nil || *fin.SummaryBytes != 3800 ||
		fin.RunCount == nil || *fin.RunCount != 1 {
		t.Fatalf("expected saved_tokens/saved_bytes/summary_bytes/run_count all present and correct, got: %+v", fin)
	}
	// Pending must be consumed (no leftover marker for the next unrelated run).
	if _, err := os.Stat(claudePendingPath(home, WorkspaceHash(workspace))); !os.IsNotExist(err) {
		t.Fatal("pending context should be consumed (removed) after a successful finish")
	}
}

func TestClaudeFinishWithoutPendingIsNoop(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	// No StartClaudeIfVSCode call happened (e.g. observe mode, or the
	// command was never actually "xit auto ..." — nothing to correlate).
	if FinishClaudeIfPending(home, workspace, FinishResult{ExitCode: 0, SavedTokens: 100, SavedBytes: 400}, time.Now()) {
		t.Fatal("expected no-op when there is no pending Claude bridge context")
	}
}

func TestClaudeFinishRejectsWorkspaceMismatch(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspaceA := filepath.Join(t.TempDir(), "workspace-a")
	workspaceB := filepath.Join(t.TempDir(), "workspace-b")
	t.Setenv("VSCODE_PID", "4242")

	if _, ok := StartClaudeIfVSCode(home, workspaceA, "xit auto go test ./...", time.Now()); !ok {
		t.Fatal("expected started bridge event")
	}
	if FinishClaudeIfPending(home, workspaceB, FinishResult{ExitCode: 0, SavedTokens: 100, SavedBytes: 400}, time.Now()) {
		t.Fatal("expected finish to be rejected for a different workspace (its own pending key never matched)")
	}
}

// TestClaudeBridgeNeverLeaksRawCommandOrOutput mirrors the equivalent Codex
// test: the Claude bridge run.started/run.finished events are plain
// hashes/integers — never the raw command text, raw cwd, or anything
// resembling captured output.
func TestClaudeBridgeNeverLeaksRawCommandOrOutput(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	rawCommand := "xit auto bash -lc 'for i in {1..1200}; do echo secret-raw-output-marker-$i; done'"
	if _, ok := StartClaudeIfVSCode(home, workspace, rawCommand, time.Now()); !ok {
		t.Fatal("expected start")
	}
	if !FinishClaudeIfPending(home, workspace, FinishResult{ExitCode: 0, SavedTokens: 5230, SavedBytes: 20920, SummaryBytes: 800, RunCount: 1}, time.Now().Add(time.Second)) {
		t.Fatal("expected finish")
	}
	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{rawCommand, "secret-raw-output-marker", workspace} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("bridge events leaked %q: %s", forbidden, string(data))
		}
	}
}

// TestClaudeAndCodexBridgesAreIsolated: starting/finishing a Claude bridge
// run in a workspace must never consume or interfere with a concurrently
// pending Codex bridge run in the SAME workspace (different pending keys:
// run-id-based for Codex, workspace-hash-based for Claude).
func TestClaudeAndCodexBridgesAreIsolated(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	codexRunID := NewRunID()
	if _, ok := StartIfCodexVSCode(home, workspace, "go test -v ./...", "codex-session-1", codexRunID, time.Now()); !ok {
		t.Fatal("expected codex started bridge event")
	}
	if _, ok := StartClaudeIfVSCode(home, workspace, "xit auto find . -maxdepth 4 -type f", time.Now()); !ok {
		t.Fatal("expected claude started bridge event")
	}

	if !FinishClaudeIfPending(home, workspace, FinishResult{ExitCode: 0, SavedTokens: 100, SavedBytes: 400}, time.Now().Add(time.Second)) {
		t.Fatal("expected claude finish to succeed independently of the pending codex run")
	}
	// Codex's pending context must still be there — untouched by the Claude finish.
	if !FinishIfPending(home, workspace, codexRunID, FinishResult{ExitCode: 0, SavedTokens: 200, SavedBytes: 800}, time.Now().Add(2*time.Second)) {
		t.Fatal("expected codex finish to still succeed after the claude bridge finished independently")
	}

	events := readEvents(t, home)
	codexEvents, claudeEvents := 0, 0
	for _, e := range events {
		switch e.Adapter {
		case AdapterClaude:
			claudeEvents++
		case Adapter:
			codexEvents++
		}
	}
	if codexEvents != 2 || claudeEvents != 2 {
		t.Fatalf("expected 2 codex + 2 claude events, got codex=%d claude=%d (events=%+v)", codexEvents, claudeEvents, events)
	}
}

func TestShellFieldsCommandHashMatchesArgv(t *testing.T) {
	command := `bash -lc 'for i in {1..1200}; do echo "vscode-codex-chat-test line=$i aaaa bbbb"; done'`
	argv := []string{"bash", "-lc", `for i in {1..1200}; do echo "vscode-codex-chat-test line=$i aaaa bbbb"; done`}
	if got, want := CommandHashFromCommand(command), CommandHashFromArgv(argv); got != want {
		t.Fatalf("command hash mismatch\ngot  %s\nwant %s", got, want)
	}
}

// TestWorkspaceHashResolvesSymlinks guards a real cross-process bug: macOS's
// os.Getwd() inside a subprocess can return the fully resolved
// "/private/var/..." form of a path while the same directory was captured
// elsewhere (Codex's hook payload cwd, or a literal path string) as its
// "/var/..." symlink form. Without resolving symlinks, the SAME real
// workspace hashes differently depending on which process produced the path
// string — breaking the one hard requirement FinishIfPending still has,
// silently dropping every run.finished event (exactly what happened in
// TestVSCodeBridgeRunCountMatchesCodexFooterTurnState before this fix).
func TestWorkspaceHashResolvesSymlinks(t *testing.T) {
	realDir := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "link-to-workspace")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	if got, want := WorkspaceHash(linkDir), WorkspaceHash(realDir); got != want {
		t.Fatalf("WorkspaceHash differs for symlink vs. real path: link=%s real=%s", got, want)
	}
}

func TestPendingContextDoesNotStoreRawSensitiveValues(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	ctx := &PendingContext{
		RunID:            "bridge-run-1",
		ThreadHash:       SHA256Hex("session-secret"),
		CommandHash:      SHA256Hex("command-secret"),
		WorkspaceHash:    WorkspaceHash("/tmp/private-workspace"),
		HostInstanceHash: HostInstanceHash("12345"),
		StartedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := WritePending(home, ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "state", "vscode-ai-bridge", "pending", ctx.RunID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"session-secret", "command-secret", "/tmp/private-workspace", "12345"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("pending context leaked %q: %s", forbidden, string(data))
		}
	}
}

// TestStartedAndFinishedEvents is the core happy path: PreToolUse generates a
// run id (NewRunID, simulated here as a literal), starts the pending context,
// and `xit auto` finishes it using that SAME run id — no thread/command hash
// matching involved, matching how the real hook -> xit auto handoff works.
func TestStartedAndFinishedEvents(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	runID := NewRunID()
	startedAt := time.Date(2026, 6, 23, 1, 2, 3, 0, time.UTC)
	ctx, ok := StartIfCodexVSCode(home, workspace, "go test -v ./...", "session-1", runID, startedAt)
	if !ok || ctx == nil {
		t.Fatal("expected started bridge event")
	}
	if ctx.RunID != runID {
		t.Fatalf("pending context run id = %q, want %q", ctx.RunID, runID)
	}
	if !FinishIfPending(home, workspace, runID, FinishResult{
		ExitCode:     0,
		SavedTokens:  3730,
		SavedBytes:   14920,
		SummaryBytes: 1280,
		RunCount:     1,
	}, startedAt.Add(time.Second)) {
		t.Fatal("expected finished bridge event")
	}
	events := readEvents(t, home)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Schema != Schema || events[0].Event != "run.started" || events[0].Host != Host || events[0].Surface != Surface || events[0].Adapter != Adapter {
		t.Fatalf("bad started event: %+v", events[0])
	}
	if events[0].RunID != runID || events[1].RunID != runID {
		t.Fatalf("started/finished run_id mismatch: started=%s finished=%s want=%s", events[0].RunID, events[1].RunID, runID)
	}
	if events[1].Event != "run.finished" || events[1].ExitCode == nil || *events[1].ExitCode != 0 ||
		events[1].SavedTokens == nil || *events[1].SavedTokens != 3730 ||
		events[1].SavedBytes == nil || *events[1].SavedBytes != 14920 {
		t.Fatalf("bad finished event: %+v", events[1])
	}
	if events[1].SummaryBytes == nil || *events[1].SummaryBytes != 1280 {
		t.Fatalf("expected summary_bytes=1280 (retained, same unit as saved_bytes for the 降噪率 ratio), got: %+v", events[1])
	}
	if events[1].RunCount == nil || *events[1].RunCount != 1 {
		t.Fatalf("expected run_count=1 (same per-turn counter as the Codex footer's 本轮共吸), got: %+v", events[1])
	}
}

// TestFinishSucceedsDespiteHostInstanceHashMismatch: VSCODE_PID can differ
// between the PreToolUse hook process (which wrote the pending context) and
// the `xit auto` subprocess (which finishes it) — different spawn points in
// the Codex agent harness. Since matching is now keyed purely by the opaque
// run id (not host_instance_hash), this must not block the finish.
func TestFinishSucceedsDespiteHostInstanceHashMismatch(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "1111")

	runID := NewRunID()
	startedAt := time.Now().UTC()
	if _, ok := StartIfCodexVSCode(home, workspace, "go test -v ./...", "session-1", runID, startedAt); !ok {
		t.Fatal("expected started bridge event")
	}

	// Simulate the finishing process seeing a different VSCODE_PID — no
	// longer read by FinishIfPending at all, so this must have zero effect.
	t.Setenv("VSCODE_PID", "9999")
	if !FinishIfPending(home, workspace, runID, FinishResult{ExitCode: 0, SavedTokens: 5230, SavedBytes: 20920}, startedAt.Add(time.Second)) {
		t.Fatal("expected finished event regardless of host_instance_hash/VSCODE_PID mismatch")
	}

	events := readEvents(t, home)
	if len(events) != 2 || events[1].Event != "run.finished" {
		t.Fatalf("expected started+finished events, got %+v", events)
	}
	if _, err := os.Stat(filepath.Join(pendingDir(home), runID+".json")); !os.IsNotExist(err) {
		t.Fatal("pending context should be consumed (removed) after a successful finish")
	}
}

// TestFinishSucceedsDespiteCommandHashMismatch: the command string PreToolUse
// observed (the AI's original Bash call) is not byte-identical to the
// argv `xit auto` actually execs (rewrite inserts env vars and may reroute
// the whole command through `xit auto`). Matching by command_hash was never
// stable; the run id is.
func TestFinishSucceedsDespiteCommandHashMismatch(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	runID := NewRunID()
	startedAt := time.Now().UTC()
	if _, ok := StartIfCodexVSCode(home, workspace, "go test -v ./...", "session-1", runID, startedAt); !ok {
		t.Fatal("expected started bridge event")
	}
	// Finish is invoked with a completely different argv than what Start saw.
	if !FinishIfPending(home, workspace, runID, FinishResult{ExitCode: 0, SavedTokens: 1000, SavedBytes: 4000}, startedAt.Add(time.Second)) {
		t.Fatal("expected finish to succeed via run id even though no command/argv is compared at all anymore")
	}
}

// TestFinishRejectsWorkspaceHashMismatch confirms workspace_hash remains a
// hard requirement even after relaxing host/command hash matching.
func TestFinishRejectsWorkspaceHashMismatch(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspaceA := filepath.Join(t.TempDir(), "workspace-a")
	workspaceB := filepath.Join(t.TempDir(), "workspace-b")
	t.Setenv("VSCODE_PID", "4242")

	runID := NewRunID()
	startedAt := time.Now().UTC()
	if _, ok := StartIfCodexVSCode(home, workspaceA, "go test -v ./...", "session-1", runID, startedAt); !ok {
		t.Fatal("expected started bridge event")
	}
	if FinishIfPending(home, workspaceB, runID, FinishResult{ExitCode: 0, SavedTokens: 100, SavedBytes: 400}, startedAt.Add(time.Second)) {
		t.Fatal("expected finish to be rejected when workspace_hash does not match")
	}
}

// TestFinishRejectsEmptyRunID: a missing/empty bridge run id (e.g. ordinary
// Codex CLI, or an AI-written `xit auto` call that never went through the
// VS Code PreToolUse rewrite) must never accidentally consume someone else's
// pending context.
func TestFinishRejectsEmptyRunID(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if FinishIfPending(home, workspace, "", FinishResult{ExitCode: 0, SavedTokens: 100, SavedBytes: 400}, time.Now()) {
		t.Fatal("expected finish to be rejected for an empty bridge run id")
	}
}

func TestFinishedFailureEvent(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	runID := NewRunID()
	now := time.Now().UTC()
	_, ok := StartIfCodexVSCode(home, workspace, "go test -v ./...", "session-1", runID, now)
	if !ok {
		t.Fatal("expected start")
	}
	if !FinishIfPending(home, workspace, runID, FinishResult{ExitCode: 1, SavedTokens: 0, SavedBytes: 0}, now.Add(time.Second)) {
		t.Fatal("expected finished failure")
	}
	events := readEvents(t, home)
	if got := *events[1].ExitCode; got != 1 {
		t.Fatalf("exit_code=%d, want 1", got)
	}
	if got := *events[1].SavedTokens; got != 0 {
		t.Fatalf("saved_tokens=%d, want 0", got)
	}
}

// TestFinishedEventNeverLeaksRawOutputOrCommand: the 降噪率/run_count fields
// added to run.finished are plain integers (saved_bytes/summary_bytes/
// run_count) — confirms the serialized event still never contains the raw
// command text or anything resembling captured command output.
func TestFinishedEventNeverLeaksRawOutputOrCommand(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	runID := NewRunID()
	now := time.Now().UTC()
	rawCommand := "bash -lc 'for i in {1..1200}; do echo secret-raw-output-marker-$i; done'"
	if _, ok := StartIfCodexVSCode(home, workspace, rawCommand, "session-secret-id", runID, now); !ok {
		t.Fatal("expected start")
	}
	if !FinishIfPending(home, workspace, runID, FinishResult{
		ExitCode:     0,
		SavedTokens:  5230,
		SavedBytes:   20920,
		SummaryBytes: 3800,
		RunCount:     1,
	}, now.Add(time.Second)) {
		t.Fatal("expected finished event")
	}
	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{rawCommand, "secret-raw-output-marker", "session-secret-id", workspace} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("bridge events leaked %q: %s", forbidden, string(data))
		}
	}
}

func TestPendingContextTTLAndRunID(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	now := time.Now().UTC()
	ctx := &PendingContext{
		RunID:            "bridge-run-ttl",
		ThreadHash:       SHA256Hex("session"),
		CommandHash:      SHA256Hex("command"),
		WorkspaceHash:    WorkspaceHash("/tmp/ws"),
		HostInstanceHash: HostInstanceHash("pid"),
		StartedAt:        now.Add(-11 * time.Minute).Format(time.RFC3339),
	}
	if err := WritePending(home, ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPending(home, ctx.RunID, now); ok {
		t.Fatal("expired pending context must be ignored")
	}

	ctx.StartedAt = now.Format(time.RFC3339)
	if err := WritePending(home, ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadPending(home, "bridge-run-other", now); ok {
		t.Fatal("wrong run id must not match")
	}
	if _, ok := ReadPending(home, ctx.RunID, now); !ok {
		t.Fatal("fresh matching pending context should match")
	}
}

func TestOrdinaryCodexCLIDoesNotGenerateBridge(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	// Ordinary Codex CLI run in a plain terminal, outside VS Code entirely:
	// VSCODE_PID is genuinely unset in that environment.
	t.Setenv("VSCODE_PID", "")
	if _, ok := StartIfCodexVSCode(home, "/tmp/ws", "go test", "session-1", NewRunID(), time.Now()); ok {
		t.Fatal("ordinary Codex CLI (no VSCODE_PID) must not start bridge")
	}
	if _, err := os.Stat(filepath.Join(home, "events", "vscode-ai-bridge.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("bridge file should not exist, err=%v", err)
	}
}

// TestStartRejectsEmptyRunID guards the PreToolUse contract: if no run id was
// generated (e.g. caller forgot to check IsCodexVSCode before generating
// one), Start must not silently write a pending context with no key.
func TestStartRejectsEmptyRunID(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("VSCODE_PID", "4242")
	if _, ok := StartIfCodexVSCode(home, "/tmp/ws", "go test", "session-1", "", time.Now()); ok {
		t.Fatal("expected Start to reject an empty run id")
	}
}

// TestStartFinishTurnIfVSCode covers the turn-LEVEL lifecycle signals used
// to drive the VS Code Bridge state machine's "守护中" (thinking) and the
// turn.finished promotion of a held "收工中" result to its final display.
func TestStartFinishTurnIfVSCode(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	if !StartTurnIfVSCode(home, workspace, AdapterClaude, SurfaceClaudeCode, time.Now()) {
		t.Fatal("expected turn.started to be recorded")
	}
	if !FinishTurnIfVSCode(home, workspace, AdapterClaude, SurfaceClaudeCode, time.Now().Add(time.Second)) {
		t.Fatal("expected turn.finished to be recorded")
	}

	events := readEvents(t, home)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Event != "turn.started" || events[0].Adapter != AdapterClaude || events[0].Surface != SurfaceClaudeCode {
		t.Fatalf("bad turn.started event: %+v", events[0])
	}
	if events[1].Event != "turn.finished" || events[1].Adapter != AdapterClaude || events[1].Surface != SurfaceClaudeCode {
		t.Fatalf("bad turn.finished event: %+v", events[1])
	}
	if events[1].FinishedAt == "" {
		t.Fatal("expected finished_at on turn.finished")
	}
	// Turn events carry no run-level data — never fabricated.
	if events[0].ExitCode != nil || events[0].SavedTokens != nil || events[1].ExitCode != nil || events[1].SavedTokens != nil {
		t.Fatalf("turn events must never carry run-level data, got: %+v / %+v", events[0], events[1])
	}
}

func TestStartFinishTurnIfVSCodeRequireVSCodeHost(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "")
	if StartTurnIfVSCode(home, workspace, Adapter, Surface, time.Now()) {
		t.Fatal("expected turn.started to be rejected outside VS Code (no VSCODE_PID)")
	}
	if FinishTurnIfVSCode(home, workspace, Adapter, Surface, time.Now()) {
		t.Fatal("expected turn.finished to be rejected outside VS Code (no VSCODE_PID)")
	}
	if _, err := os.Stat(filepath.Join(home, "events", "vscode-ai-bridge.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("bridge file should not exist, err=%v", err)
	}
}

// TestTurnEventsNeverLeakRawData mirrors the run-level leak guards: turn
// events are schema/hash/timestamp only.
func TestTurnEventsNeverLeakRawData(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "secret-workspace-name")
	t.Setenv("VSCODE_PID", "4242")

	if !StartTurnIfVSCode(home, workspace, Adapter, Surface, time.Now()) {
		t.Fatal("expected turn.started")
	}
	if !FinishTurnIfVSCode(home, workspace, Adapter, Surface, time.Now().Add(time.Second)) {
		t.Fatal("expected turn.finished")
	}
	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), workspace) || strings.Contains(string(data), "secret-workspace-name") {
		t.Fatalf("turn events leaked the raw workspace path: %s", data)
	}
}

// TestHostInstanceHashIsWorkspaceIndependent locks in the fix: two commands
// run from the same VS Code window (same VSCODE_PID) but different cwds must
// produce the SAME host_instance_hash, so the extension can recognize "same
// window" even when the AI cd'd to a different project mid-session.
func TestHostInstanceHashIsWorkspaceIndependent(t *testing.T) {
	if HostInstanceHash("4242") != HostInstanceHash("4242") {
		t.Fatal("expected the same pid to always produce the same host_instance_hash")
	}
	if HostInstanceHash("4242") == HostInstanceHash("9999") {
		t.Fatal("expected different pids to produce different host_instance_hash")
	}
}

// TestAppendEventMirrorsToGlobalHome: AppendEvent must also write a copy of
// every bridge event into MirrorHome() (XIT_VSCODE_BRIDGE_HOME in this test,
// via TestMain — shared across this whole package's test run, so other
// tests' events legitimately accumulate there too; this test only checks
// that ITS OWN event, identified by its unique RunID, shows up) in addition
// to the per-project home it's given — this is what lets a VS Code window
// watching its own workspace's mirror copy observe an event whose primary
// copy landed in a different project's .xit dir because the AI's cwd
// differed from the VS Code workspace root.
func TestAppendEventMirrorsToGlobalHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	if _, ok := StartClaudeIfVSCode(home, workspace, "xit auto go test ./...", time.Now()); !ok {
		t.Fatal("expected started bridge event")
	}

	primary := readEvents(t, home)
	if len(primary) != 1 {
		t.Fatalf("expected 1 primary event, got %d", len(primary))
	}

	var found *Event
	for _, e := range readEvents(t, MirrorHome()) {
		if e.RunID == primary[0].RunID {
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatalf("expected an event with run_id %s to be mirrored into MirrorHome()", primary[0].RunID)
	}
	if found.WorkspaceHash != primary[0].WorkspaceHash {
		t.Fatalf("mirrored event does not match primary event: mirror=%+v primary=%+v", found, primary[0])
	}
}

// TestAppendEventDoesNotDuplicateWhenHomeIsAlreadyTheMirror: if a caller's
// home already resolves to the same path as MirrorHome() (e.g. XIT_HOME and
// XIT_VSCODE_BRIDGE_HOME both point at the same directory), the event must
// be appended exactly once, not twice.
func TestAppendEventDoesNotDuplicateWhenHomeIsAlreadyTheMirror(t *testing.T) {
	shared := filepath.Join(t.TempDir(), ".xit")
	t.Setenv("XIT_VSCODE_BRIDGE_HOME", shared)
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	if !StartTurnIfVSCode(shared, workspace, AdapterClaude, SurfaceClaudeCode, time.Now()) {
		t.Fatal("expected turn.started to be recorded")
	}
	events := readEvents(t, shared)
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event (no duplicate write), got %d", len(events))
	}
}

func readEvents(t *testing.T, home string) []Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "events", "vscode-ai-bridge.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}
