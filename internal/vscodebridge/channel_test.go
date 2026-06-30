package vscodebridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChannelIDIsAnonymousStableAndExcludesRaw(t *testing.T) {
	a := ChannelID("claude", "claude_code", SHA256Hex("4242"), WorkspaceHash("/tmp/ws"))
	b := ChannelID("claude", "claude_code", SHA256Hex("4242"), WorkspaceHash("/tmp/ws"))
	if a != b {
		t.Fatalf("ChannelID must be stable for identical inputs: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("ChannelID must be a sha256 hex (64 chars), got %d", len(a))
	}
	// Different adapter => different channel.
	if c := ChannelID("codex", "codex_chat", SHA256Hex("4242"), WorkspaceHash("/tmp/ws")); c == a {
		t.Fatal("different adapter/surface must yield a different channel id")
	}
	// No raw signal leaks into the hash output.
	for _, raw := range []string{"claude", "claude_code", "/tmp/ws", "4242"} {
		if strings.Contains(a, raw) {
			t.Fatalf("ChannelID must not contain raw signal %q", raw)
		}
	}
}

// TestAppendEventStampsChannelAndEventID verifies the additive multi-channel
// fields are populated, that the two events of one task share a channel id,
// and that the primary + mirror dual-write of the SAME event carry the SAME
// event_id (so the extension can dedupe them).
func TestAppendEventStampsChannelAndEventID(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".xit")
	workspace := filepath.Join(t.TempDir(), "workspace")
	t.Setenv("VSCODE_PID", "4242")

	startedAt := time.Now().UTC()
	if _, ok := StartClaudeIfVSCode(home, workspace, "xit auto go test ./...", startedAt); !ok {
		t.Fatal("expected started bridge event")
	}
	if !FinishClaudeIfPending(home, workspace, FinishResult{ExitCode: 0, SavedBytes: 4000, SummaryBytes: 1000, RunCount: 1}, startedAt.Add(time.Second)) {
		t.Fatal("expected finished bridge event")
	}

	events := readEvents(t, home)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.ChannelID == "" {
			t.Fatalf("event %d missing channel_id", i)
		}
		if ev.EventID == "" {
			t.Fatalf("event %d missing event_id", i)
		}
	}
	if events[0].ChannelID != events[1].ChannelID {
		t.Fatalf("both events of one task must share a channel id: %s != %s", events[0].ChannelID, events[1].ChannelID)
	}
	want := ChannelID(AdapterClaude, SurfaceClaudeCode, HostInstanceHash("4242"), WorkspaceHash(workspace))
	if events[0].ChannelID != want {
		t.Fatalf("channel id mismatch: got %s want %s", events[0].ChannelID, want)
	}
	// Per-event ids must differ between the two distinct events.
	if events[0].EventID == events[1].EventID {
		t.Fatal("distinct events must have distinct event_ids")
	}

	// Mirror dual-write: the SAME logical event must carry the SAME event_id
	// in both the primary file and the mirror file, so the extension's
	// primary/mirror dedupe works.
	mirror := os.Getenv("XIT_VSCODE_BRIDGE_HOME")
	mirrorEvents := readEvents(t, mirror)
	if len(mirrorEvents) < 2 {
		t.Fatalf("expected mirror to receive >=2 events, got %d", len(mirrorEvents))
	}
	// Find the started event in the mirror matching our channel and compare ids.
	var matched bool
	for _, me := range mirrorEvents {
		if me.EventID == events[0].EventID {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatal("mirror must contain the same event_id as the primary (shared dual-write id)")
	}
}

// TestOldV1EventStillParses guarantees backward compatibility: an event
// serialized by an OLD CLI (no channel_id/turn_id/event_id) still unmarshals
// into the current Event struct with every required field intact.
func TestOldV1EventStillParses(t *testing.T) {
	old := `{"schema":"xit.vscode-ai-bridge.v1","event":"run.finished","host":"vscode","surface":"claude_code","adapter":"claude","workspace_hash":"` +
		WorkspaceHash("/tmp/ws") + `","host_instance_hash":"` + HostInstanceHash("4242") +
		`","run_id":"bridge-old","command_hash":"` + CommandHashFromCommand("go test ./...") +
		`","started_at":"2026-06-30T10:00:00Z","finished_at":"2026-06-30T10:00:05Z","exit_code":0,"saved_bytes":4000}`

	var ev Event
	if err := json.Unmarshal([]byte(old), &ev); err != nil {
		t.Fatalf("old v1 event must still parse: %v", err)
	}
	if ev.Schema != Schema || ev.Event != "run.finished" || ev.RunID != "bridge-old" {
		t.Fatalf("required fields not preserved: %+v", ev)
	}
	if ev.ChannelID != "" || ev.TurnID != "" || ev.EventID != "" {
		t.Fatal("old event must leave the new optional fields empty, not error")
	}
	if ev.ExitCode == nil || *ev.ExitCode != 0 || ev.SavedBytes == nil || *ev.SavedBytes != 4000 {
		t.Fatalf("optional result fields lost: %+v", ev)
	}
}
