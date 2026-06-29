package vscodebridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Schema     = "xit.vscode-ai-bridge.v1"
	Host       = "vscode"
	Surface    = "codex_chat"
	Adapter    = "codex"
	pendingTTL = 10 * time.Minute

	// SurfaceClaudeCode/AdapterClaude are the Claude Code VS Code panel's
	// equivalents of Surface/Adapter above. Kept as separate constants
	// (rather than renaming Surface/Adapter) so the already-verified Codex
	// bridge code and tests never change their identifiers.
	SurfaceClaudeCode = "claude_code"
	AdapterClaude     = "claude"
)

// Env carries only signals that are genuinely ambient in a Codex subprocess
// running somewhere inside VS Code's process tree. Earlier versions also
// required CODEX_INTERNAL_ORIGINATOR_OVERRIDE and CODEX_THREAD_ID, but nothing
// in the real Codex/VS Code pipeline ever sets either one — only test code
// did via t.Setenv — so IsCodexVSCode() always returned false in production
// and the bridge never started a single real run. VSCODE_PID is the only
// signal that's actually real: VS Code sets it for every child process in its
// tree (integrated terminal or extension host), including a Codex CLI
// subprocess.
type Env struct {
	VSCodePID string
}

// PendingContext is keyed and looked up by RunID alone (see StartIfCodexVSCode
// / FinishIfPending) — not by thread_hash+command_hash. The PreToolUse hook
// process (which writes the pending context) and the `xit auto` subprocess
// (which finishes it) only reliably share one thing: the opaque run id
// injected into the rewritten command's env prefix (XIT_VSCODE_BRIDGE_RUN_ID).
// command_hash/thread_hash are kept here purely for event diagnostics, never
// for matching — Codex's PreToolUse rewrite can legitimately change the
// command string (injecting xit auto + env vars) between the moment the hook
// observes it and the moment `xit auto` actually runs, so hashing "the
// command" was never a stable correlation key to begin with.
type PendingContext struct {
	RunID            string `json:"run_id"`
	ThreadHash       string `json:"thread_hash,omitempty"`
	CommandHash      string `json:"command_hash,omitempty"`
	WorkspaceHash    string `json:"workspace_hash"`
	HostInstanceHash string `json:"host_instance_hash"`
	StartedAt        string `json:"started_at"`
}

type Event struct {
	Schema           string `json:"schema"`
	Event            string `json:"event"`
	Host             string `json:"host"`
	Surface          string `json:"surface"`
	Adapter          string `json:"adapter"`
	WorkspaceHash    string `json:"workspace_hash"`
	HostInstanceHash string `json:"host_instance_hash"`
	ThreadHash       string `json:"thread_hash,omitempty"`
	RunID            string `json:"run_id"`
	CommandHash      string `json:"command_hash,omitempty"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	SavedTokens      *int   `json:"saved_tokens,omitempty"`
	SavedBytes       *int   `json:"saved_bytes,omitempty"`
	// SummaryBytes is the retained byte count after filtering (i.e. what's
	// left over, not what was removed) — paired with SavedBytes (same unit:
	// bytes) lets the VS Code Dashboard compute a real 降噪率 ratio
	// (saved / (saved + retained)) instead of showing a fabricated number.
	SummaryBytes *int `json:"summary_bytes,omitempty"`
	// RunCount is the same per-turn counter the Codex CLI footer reports as
	// "本轮共吸 N次" — never a different (e.g. today's total) count.
	RunCount *int `json:"run_count,omitempty"`
}

// FinishResult carries the non-sensitive outcome fields FinishIfPending needs
// to append a run.finished event. No raw output, raw command, raw cwd, or
// full thread/session id is ever included here. RunCount is the same
// per-turn counter the Codex CLI footer reports as "本轮共吸 N次" — 0 for
// call sites that run before Codex's turn counter is ever incremented (e.g.
// an early setup failure), which also matches what the footer itself would
// show for that case (unchanged from before this invocation).
type FinishResult struct {
	ExitCode     int
	SavedTokens  int
	SavedBytes   int
	SummaryBytes int
	RunCount     int
}

func CurrentEnv() Env {
	return Env{VSCodePID: os.Getenv("VSCODE_PID")}
}

func IsCodexVSCode(env Env) bool {
	return env.VSCodePID != ""
}

// IsVSCodeHost is the same VSCODE_PID check as IsCodexVSCode, named for
// adapters beyond Codex (Claude Code, etc.) where "IsCodexVSCode" would be a
// misnomer. Behavior is identical; kept as a separate function so Codex's
// call sites never need to change.
func IsVSCodeHost(env Env) bool {
	return env.VSCodePID != ""
}

func SHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func NormalizeWorkspace(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	// Resolve symlinks so workspace_hash is stable regardless of which
	// equivalent form of the same directory a caller passes — e.g. on
	// macOS, os.Getwd() inside a subprocess can return the fully resolved
	// "/private/var/..." form while a path captured earlier (Codex's hook
	// payload cwd, or in tests, t.TempDir()) is the "/var/..." symlink form.
	// Without this, the SAME real workspace hashes differently depending on
	// which process/source produced the path string, breaking the one hard
	// requirement FinishIfPending still has. Falls back to the abs+clean
	// form if the path doesn't exist yet (can't resolve symlinks on it).
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func WorkspaceHash(workspace string) string {
	return SHA256Hex(NormalizeWorkspace(workspace))
}

// HostInstanceHash intentionally depends on pid alone, not workspace. It is
// the one signal meant to answer "is this the same VS Code window?"
// independent of which directory the command that produced the event
// happened to run in — e.g. the AI cd'd to a different project mid-session.
// Folding workspace into this hash (as an earlier version did) made it
// impossible to ever use for that purpose, since it would then differ
// between two commands run from different cwds in the very same window.
func HostInstanceHash(pid string) string {
	return SHA256Hex(pid)
}

func ThreadHash(threadID string) string {
	return SHA256Hex(threadID)
}

func CommandHashFromArgv(argv []string) string {
	return SHA256Hex(strings.Join(argv, "\x00"))
}

func CommandHashFromCommand(command string) string {
	return CommandHashFromArgv(ShellFields(command))
}

// NewRunID generates an opaque, unguessable run id. The PreToolUse hook calls
// this BEFORE knowing whether a rewrite will actually happen (so it can
// inject the id into the env prefix in the same pass as session/turn id);
// callers must discard it if no rewrite occurs.
func NewRunID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "bridge-" + SHA256Hex(time.Now().UTC().Format(time.RFC3339Nano))[:24]
	}
	return "bridge-" + hex.EncodeToString(b[:])
}

// StartIfCodexVSCode records a pending bridge run keyed by runID (generated
// by the caller via NewRunID and also injected into the rewritten command as
// XIT_VSCODE_BRIDGE_RUN_ID) and appends a run.started event. sessionID is
// Codex's own hook-protocol session id (in.SessionID), used only for the
// diagnostic thread_hash — never stored raw.
func StartIfCodexVSCode(home, workspace, command, sessionID, runID string, now time.Time) (*PendingContext, bool) {
	env := CurrentEnv()
	if !IsCodexVSCode(env) || runID == "" {
		return nil, false
	}
	ctx := &PendingContext{
		RunID:            runID,
		ThreadHash:       ThreadHash(sessionID),
		CommandHash:      CommandHashFromCommand(command),
		WorkspaceHash:    WorkspaceHash(workspace),
		HostInstanceHash: HostInstanceHash(env.VSCodePID),
		StartedAt:        now.UTC().Format(time.RFC3339),
	}
	_ = CleanupExpired(home, now)
	if WritePending(home, ctx) != nil {
		return nil, false
	}
	_ = AppendEvent(home, Event{
		Schema:           Schema,
		Event:            "run.started",
		Host:             Host,
		Surface:          Surface,
		Adapter:          Adapter,
		WorkspaceHash:    ctx.WorkspaceHash,
		HostInstanceHash: ctx.HostInstanceHash,
		ThreadHash:       ctx.ThreadHash,
		RunID:            ctx.RunID,
		CommandHash:      ctx.CommandHash,
		StartedAt:        ctx.StartedAt,
	})
	return ctx, true
}

// FinishIfPending consumes the pending context for bridgeRunID (read from
// XIT_VSCODE_BRIDGE_RUN_ID by the `xit auto` subprocess) and appends a
// run.finished event. workspace_hash is still a hard requirement — the only
// other signal that scopes a run to "this project" — but bridgeRunID itself
// is already an unguessable, single-use correlation key, so neither
// thread_hash, command_hash, nor host_instance_hash need to match for the
// finish to be accepted: VSCODE_PID and the exact command string are not
// reliably shared between the hook process and this subprocess, but the run
// id (passed explicitly through the rewritten command's env, not derived from
// ambient state) is.
func FinishIfPending(home, workspace, bridgeRunID string, result FinishResult, now time.Time) bool {
	if bridgeRunID == "" {
		return false
	}
	ctx, ok := ReadPending(home, bridgeRunID, now)
	if !ok || ctx.WorkspaceHash != WorkspaceHash(workspace) {
		return false
	}
	finished := now.UTC().Format(time.RFC3339)
	event := Event{
		Schema:           Schema,
		Event:            "run.finished",
		Host:             Host,
		Surface:          Surface,
		Adapter:          Adapter,
		WorkspaceHash:    ctx.WorkspaceHash,
		HostInstanceHash: ctx.HostInstanceHash,
		ThreadHash:       ctx.ThreadHash,
		RunID:            ctx.RunID,
		CommandHash:      ctx.CommandHash,
		StartedAt:        ctx.StartedAt,
		FinishedAt:       finished,
		ExitCode:         &result.ExitCode,
		SavedTokens:      &result.SavedTokens,
		SavedBytes:       &result.SavedBytes,
		SummaryBytes:     &result.SummaryBytes,
		RunCount:         &result.RunCount,
	}
	_ = AppendEvent(home, event)
	_ = RemovePending(home, bridgeRunID)
	return true
}

func pendingDir(home string) string {
	return filepath.Join(home, "state", "vscode-ai-bridge", "pending")
}

func pendingPath(home, runID string) string {
	return filepath.Join(pendingDir(home), runID+".json")
}

func writePendingFile(path string, ctx *PendingContext) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(ctx)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readPendingFile(path string, now time.Time) (*PendingContext, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var ctx PendingContext
	if json.Unmarshal(data, &ctx) != nil {
		return nil, false
	}
	started, err := time.Parse(time.RFC3339, ctx.StartedAt)
	if err != nil || now.Sub(started) > pendingTTL || now.Before(started.Add(-1*time.Minute)) {
		_ = os.Remove(path)
		return nil, false
	}
	return &ctx, true
}

func WritePending(home string, ctx *PendingContext) error {
	return writePendingFile(pendingPath(home, ctx.RunID), ctx)
}

func ReadPending(home, runID string, now time.Time) (*PendingContext, bool) {
	ctx, ok := readPendingFile(pendingPath(home, runID), now)
	if !ok || ctx.RunID != runID {
		return nil, false
	}
	return ctx, true
}

func RemovePending(home, runID string) error {
	return os.Remove(pendingPath(home, runID))
}

// claudePendingPath is keyed by workspace_hash, not by an opaque run id.
// Unlike Codex, Claude Code's PreToolUse hook protocol has no documented way
// to mutate the Bash tool_input, so there is no channel to hand an opaque id
// to the `xit auto` subprocess via an injected env var. Bash tool calls
// inside one Claude Code session run sequentially, so "the one fresh pending
// entry for this workspace" is an acceptable, fail-open correlation key —
// the same tradeoff XiT already accepts elsewhere for best-effort signals.
func claudePendingPath(home, workspaceHash string) string {
	return filepath.Join(pendingDir(home), "claude-"+workspaceHash+".json")
}

// StartClaudeIfVSCode records a pending Claude Code VS Code bridge run for
// workspace and appends a run.started event. Called from the PreToolUse hook
// when it observes a Bash command that is already "xit auto ..." (the AI's
// own command, or a retry after a reroute recommendation) while VSCODE_PID
// is ambiently present. command is hashed only (diagnostic command_hash,
// never used for matching) — never stored raw.
func StartClaudeIfVSCode(home, workspace, command string, now time.Time) (*PendingContext, bool) {
	env := CurrentEnv()
	if !IsVSCodeHost(env) {
		return nil, false
	}
	wsHash := WorkspaceHash(workspace)
	ctx := &PendingContext{
		RunID:            NewRunID(),
		CommandHash:      CommandHashFromCommand(command),
		WorkspaceHash:    wsHash,
		HostInstanceHash: HostInstanceHash(env.VSCodePID),
		StartedAt:        now.UTC().Format(time.RFC3339),
	}
	_ = CleanupExpired(home, now)
	if writePendingFile(claudePendingPath(home, wsHash), ctx) != nil {
		return nil, false
	}
	_ = AppendEvent(home, Event{
		Schema:           Schema,
		Event:            "run.started",
		Host:             Host,
		Surface:          SurfaceClaudeCode,
		Adapter:          AdapterClaude,
		WorkspaceHash:    ctx.WorkspaceHash,
		HostInstanceHash: ctx.HostInstanceHash,
		RunID:            ctx.RunID,
		CommandHash:      ctx.CommandHash,
		StartedAt:        ctx.StartedAt,
	})
	return ctx, true
}

// FinishClaudeIfPending consumes the pending Claude Code VS Code bridge
// context for workspace (if any, and still fresh) and appends a run.finished
// event. No-op (returns false) when there is no matching pending entry —
// e.g. ordinary Claude CLI usage outside VS Code, or observe-mode where the
// command was never actually routed through `xit auto`.
func FinishClaudeIfPending(home, workspace string, result FinishResult, now time.Time) bool {
	wsHash := WorkspaceHash(workspace)
	path := claudePendingPath(home, wsHash)
	ctx, ok := readPendingFile(path, now)
	if !ok {
		return false
	}
	finished := now.UTC().Format(time.RFC3339)
	event := Event{
		Schema:           Schema,
		Event:            "run.finished",
		Host:             Host,
		Surface:          SurfaceClaudeCode,
		Adapter:          AdapterClaude,
		WorkspaceHash:    ctx.WorkspaceHash,
		HostInstanceHash: ctx.HostInstanceHash,
		RunID:            ctx.RunID,
		CommandHash:      ctx.CommandHash,
		StartedAt:        ctx.StartedAt,
		FinishedAt:       finished,
		ExitCode:         &result.ExitCode,
		SavedTokens:      &result.SavedTokens,
		SavedBytes:       &result.SavedBytes,
		SummaryBytes:     &result.SummaryBytes,
		RunCount:         &result.RunCount,
	}
	_ = AppendEvent(home, event)
	_ = os.Remove(path)
	return true
}

// StartTurnIfVSCode/FinishTurnIfVSCode emit turn-LEVEL lifecycle signals —
// distinct from run.started/run.finished, which are per-tool-call. A turn
// is "the AI started thinking about a new user prompt" / "the AI's final
// answer for this turn is done". Unlike the run-level events, these are
// pure broadcast signals with no pending-file pairing: the VS Code
// extension's own state machine decides what (if anything) to do when one
// arrives (e.g. show "正在守护" on turn.started, promote a held
// run.finished result to its final display on turn.finished). Scoped by
// workspace_hash only, like the rest of this schema — no raw prompt,
// assistant reply, or session/thread id is ever included.
func StartTurnIfVSCode(home, workspace, adapter, surface string, now time.Time) bool {
	env := CurrentEnv()
	if !IsVSCodeHost(env) {
		return false
	}
	_ = AppendEvent(home, Event{
		Schema:           Schema,
		Event:            "turn.started",
		Host:             Host,
		Surface:          surface,
		Adapter:          adapter,
		WorkspaceHash:    WorkspaceHash(workspace),
		HostInstanceHash: HostInstanceHash(env.VSCodePID),
		RunID:            NewRunID(),
		StartedAt:        now.UTC().Format(time.RFC3339),
	})
	return true
}

func FinishTurnIfVSCode(home, workspace, adapter, surface string, now time.Time) bool {
	env := CurrentEnv()
	if !IsVSCodeHost(env) {
		return false
	}
	ts := now.UTC().Format(time.RFC3339)
	_ = AppendEvent(home, Event{
		Schema:           Schema,
		Event:            "turn.finished",
		Host:             Host,
		Surface:          surface,
		Adapter:          adapter,
		WorkspaceHash:    WorkspaceHash(workspace),
		HostInstanceHash: HostInstanceHash(env.VSCodePID),
		RunID:            NewRunID(),
		StartedAt:        ts,
		FinishedAt:       ts,
	})
	return true
}

func CleanupExpired(home string, now time.Time) error {
	dir := pendingDir(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var ctx PendingContext
		if json.Unmarshal(data, &ctx) != nil {
			_ = os.Remove(path)
			continue
		}
		started, err := time.Parse(time.RFC3339, ctx.StartedAt)
		if err != nil || now.Sub(started) > pendingTTL {
			_ = os.Remove(path)
		}
	}
	return nil
}

// MirrorHome is a second, workspace-independent home that AppendEvent always
// also writes bridge events to (see appendEventFile). The Go-side `home`
// passed into every Start*/Finish* function above is derived from the
// command's own cwd (see resolveClaudeHome/resolveCodexHome in the hook
// packages) — correct for run-history/state, but it means the bridge event
// itself lands in whichever project the AI's cwd happened to be in, not
// necessarily the project the VS Code window watching for it has open. The
// mirror gives the extension's bridge watcher a second, cwd-independent
// place to find the same event (see classifyBridgeEvent on the TS side,
// which still scopes acceptance by workspace_hash/host_instance_hash —
// mirroring does not by itself relax any privacy/isolation guarantee).
// XIT_VSCODE_BRIDGE_HOME overrides the default (tests use this to avoid
// writing into the real machine's home directory); empty return disables
// the mirror rather than erroring.
func MirrorHome() string {
	if v := os.Getenv("XIT_VSCODE_BRIDGE_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".xit")
}

func samePath(a, b string) bool {
	ra, errA := filepath.Abs(a)
	rb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

func appendEventFile(home string, event Event) error {
	path := filepath.Join(home, "events", "vscode-ai-bridge.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func AppendEvent(home string, event Event) error {
	err := appendEventFile(home, event)
	if mirror := MirrorHome(); mirror != "" && !samePath(mirror, home) {
		_ = appendEventFile(mirror, event)
	}
	return err
}
