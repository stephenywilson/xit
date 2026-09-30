package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// forbiddenKeys are payload fields that must NEVER appear on the wire.
var forbiddenKeys = []string{
	"raw_output", "raw_log", "prompt", "ai_reply", "command",
	"cwd", "path", "file_name", "repo_name", "username", "email",
	"api_key", "token", "full_session_id", "full_host_instance_hash",
	"full_workspace_hash",
	"channel_id", "run_id", "turn_id", "event_id",
	"full_channel_id", "full_run_id", "full_turn_id",
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	home := t.TempDir()
	c := NewClient(home, "0.2.49")
	c.now = func() time.Time { return time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC) }
	return c
}

// TestPayloadHasNoSensitiveFields is the core privacy guarantee: even when a
// caller would love to attach command/cwd/output, the marshalled event cannot
// contain any forbidden key.
func TestPayloadHasNoSensitiveFields(t *testing.T) {
	c := newTestClient(t)
	ev := c.Build(Metrics{
		Event:        "run.finished",
		Adapter:      "claude",
		Surface:      "cli",
		InputBytes:   100000,
		SummaryBytes: 1000,
		SavedBytes:   99000,
		RunCount:     1,
		Status:       "success",
		ErrorKind:    "none",
	})
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range forbiddenKeys {
		if _, ok := m[k]; ok {
			t.Fatalf("forbidden key %q present in telemetry payload: %s", k, data)
		}
	}
	// Also assert the literal substrings never leak (covers nested/renamed cases).
	lower := strings.ToLower(string(data))
	for _, bad := range []string{"raw_output", "\"prompt\"", "\"command\"", "\"cwd\"", "api_key"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("payload leaked %q: %s", bad, data)
		}
	}
}

func TestAllowedKeysOnly(t *testing.T) {
	c := newTestClient(t)
	ev := c.Build(Metrics{Adapter: "codex", Surface: "hook", InputBytes: 10, SavedBytes: 5})
	data, _ := json.Marshal(ev)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	allowed := map[string]bool{
		"schema": true, "event": true, "anonymous_install_id": true, "timestamp": true,
		"cli_version": true, "vscode_extension_version": true, "adapter": true, "surface": true,
		"os": true, "arch": true, "input_bytes": true, "summary_bytes": true, "saved_bytes": true,
		"estimated_saved_tokens": true, "compression_ratio": true, "run_count": true,
		"status": true, "error_kind": true,
	}
	for k := range m {
		if !allowed[k] {
			t.Fatalf("unexpected key in payload: %q", k)
		}
	}
}

func TestSavedTokensEstimate(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0}, {3, 0}, {4, 1}, {100, 25}, {122222, 30555}, {-10, 0},
	}
	for _, tc := range cases {
		if got := EstimatedSavedTokens(tc.in); got != tc.want {
			t.Fatalf("EstimatedSavedTokens(%d)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestCompressionRatio(t *testing.T) {
	if r := CompressionRatio(100000, 92000); r < 0.91 || r > 0.93 {
		t.Fatalf("ratio=%v want ~0.92", r)
	}
	if r := CompressionRatio(0, 10); r != 0 {
		t.Fatalf("ratio with zero input should be 0, got %v", r)
	}
	if r := CompressionRatio(10, 999); r != 1 {
		t.Fatalf("ratio should clamp to 1, got %v", r)
	}
}

func TestAdapterAndSurfaceNormalization(t *testing.T) {
	c := newTestClient(t)
	ev := c.Build(Metrics{Adapter: "antigravity", Surface: "weird"})
	if ev.Adapter != "unknown" {
		t.Fatalf("non-listed adapter should be 'unknown', got %q", ev.Adapter)
	}
	if ev.Surface != "cli" {
		t.Fatalf("non-listed surface should fall back to 'cli', got %q", ev.Surface)
	}
}

// TestCodexSurfaceValuesPassThrough locks in backward-compatible support for
// the finer-grained Codex front-end breakdown (0.2.51): adapter stays
// "codex" for all of them, and each of the new surface values normalizes to
// itself rather than collapsing to the generic "cli" fallback.
func TestCodexSurfaceValuesPassThrough(t *testing.T) {
	c := newTestClient(t)
	for _, surface := range []string{"codex_cli", "codex_ide", "chatgpt_desktop_codex", "codex_shared"} {
		ev := c.Build(Metrics{Adapter: "codex", Surface: surface})
		if ev.Adapter != "codex" {
			t.Fatalf("surface=%s: adapter should stay codex, got %q", surface, ev.Adapter)
		}
		if ev.Surface != surface {
			t.Fatalf("surface=%s: expected pass-through, got %q", surface, ev.Surface)
		}
	}
}

func TestEnabledEnvOverrideHighestPriority(t *testing.T) {
	home := t.TempDir()
	// Persist enabled=true in the file, then force off via env.
	if err := SetEnabled(home, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XIT_TELEMETRY", "off")
	if Enabled(home) {
		t.Fatal("XIT_TELEMETRY=off must win over config")
	}
	t.Setenv("XIT_TELEMETRY", "on")
	if !Enabled(home) {
		t.Fatal("XIT_TELEMETRY=on must enable")
	}
}

func TestDefaultDisabled(t *testing.T) {
	home := t.TempDir()
	os.Unsetenv("XIT_TELEMETRY")
	os.Unsetenv("DO_NOT_TRACK")
	if Enabled(home) {
		t.Fatal("telemetry should be disabled by default (opt-in)")
	}
	en, src := EnabledSource(home)
	if en || src != "default off" {
		t.Fatalf("expected en=false src='default off', got en=%v src=%q", en, src)
	}
}

func TestSetEnabledExplicitOptIn(t *testing.T) {
	home := t.TempDir()
	os.Unsetenv("XIT_TELEMETRY")
	os.Unsetenv("DO_NOT_TRACK")
	if err := SetEnabled(home, true); err != nil {
		t.Fatal(err)
	}
	if !Enabled(home) {
		t.Fatal("after SetEnabled(true), telemetry should be on")
	}
	en, src := EnabledSource(home)
	if !en || !strings.Contains(src, "versioned local consent") {
		t.Fatalf("expected versioned local consent, got en=%v src=%q", en, src)
	}
}

func TestSetEnabledPersists(t *testing.T) {
	home := t.TempDir()
	os.Unsetenv("XIT_TELEMETRY")
	os.Unsetenv("DO_NOT_TRACK")
	if err := SetEnabled(home, true); err != nil {
		t.Fatal(err)
	}
	if !Enabled(home) {
		t.Fatal("after SetEnabled(true), telemetry should be on")
	}
	if err := SetEnabled(home, false); err != nil {
		t.Fatal(err)
	}
	if Enabled(home) {
		t.Fatal("after SetEnabled(false), telemetry should be off")
	}
	en, src := EnabledSource(home)
	if en || !strings.Contains(src, "telemetry.json") {
		t.Fatalf("expected disabled-from-config, got en=%v src=%q", en, src)
	}
}

func TestNewClientDisabledDoesNotPersistInstallID(t *testing.T) {
	home := t.TempDir()
	os.Unsetenv("XIT_TELEMETRY")
	os.Unsetenv("DO_NOT_TRACK")
	_ = NewClient(home, "0.2.53")
	if _, err := os.Stat(statePath(home)); !os.IsNotExist(err) {
		t.Fatal("NewClient must not create telemetry.json when telemetry is disabled")
	}
	if id := CurrentInstallID(home); id != "" {
		t.Fatalf("CurrentInstallID must be empty when not initialized, got %q", id)
	}
}

func TestCurrentInstallIDReadOnly(t *testing.T) {
	home := t.TempDir()
	id := CurrentInstallID(home)
	if id != "" {
		t.Fatalf("expected empty install id, got %q", id)
	}
	if _, err := os.Stat(statePath(home)); !os.IsNotExist(err) {
		t.Fatal("CurrentInstallID must be read-only and not create telemetry.json")
	}
}

func TestDoNotTrackPrecedence(t *testing.T) {
	home := t.TempDir()
	_ = SetEnabled(home, true)
	if !Enabled(home) {
		t.Fatal("should be enabled")
	}
	t.Setenv("DO_NOT_TRACK", "1")
	if Enabled(home) {
		t.Fatal("DO_NOT_TRACK=1 must override local consent")
	}
	t.Setenv("XIT_TELEMETRY", "on")
	if !Enabled(home) {
		t.Fatal("XIT_TELEMETRY=on must override DO_NOT_TRACK=1")
	}
}

func TestEmitDisabledSendsNothing(t *testing.T) {
	home := t.TempDir()
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	t.Setenv("XIT_TELEMETRY", "off")
	t.Setenv("XIT_API_BASE", srv.URL)
	c := NewClient(home, "0.2.49")
	c.Emit(Metrics{Adapter: "cli", Surface: "cli"})
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("telemetry off must not send, got %d hits", hits)
	}
}

func TestEmitEnabledPostsEvent(t *testing.T) {
	home := t.TempDir()
	got := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		got <- buf
		w.WriteHeader(202)
	}))
	defer srv.Close()

	t.Setenv("XIT_TELEMETRY", "on")
	t.Setenv("XIT_API_BASE", srv.URL)
	c := NewClient(home, "0.2.49")
	c.Emit(Metrics{Adapter: "codex", Surface: "hook", InputBytes: 100, SavedBytes: 80, Status: "success"})

	select {
	case body := <-got:
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("server got invalid json: %v (%s)", err, body)
		}
		for _, k := range forbiddenKeys {
			if _, ok := m[k]; ok {
				t.Fatalf("forbidden key %q reached the server", k)
			}
		}
		if m["adapter"] != "codex" {
			t.Fatalf("adapter mismatch: %v", m["adapter"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive telemetry event")
	}
}

func TestEmitFailOpenSpoolsToQueue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XIT_TELEMETRY", "on")
	t.Setenv("XIT_API_BASE", "http://127.0.0.1:0") // unroutable => delivery fails
	c := NewClient(home, "0.2.49")
	c.deliver(c.Build(Metrics{Adapter: "kimi", Surface: "cli"}))
	q := readQueue(home)
	if len(q) != 1 {
		t.Fatalf("failed delivery should spool 1 event, got %d", len(q))
	}
}

func TestQueueCappedAt100(t *testing.T) {
	home := t.TempDir()
	for i := 0; i < 150; i++ {
		enqueue(home, Event{Schema: SchemaName, Adapter: "cli"})
	}
	if got := len(readQueue(home)); got != maxQueue {
		t.Fatalf("queue should cap at %d, got %d", maxQueue, got)
	}
}

func TestInstallIDStableAndAnonymous(t *testing.T) {
	home := t.TempDir()
	id1 := InstallID(home)
	id2 := InstallID(home)
	if id1 != id2 {
		t.Fatalf("install id should be stable: %q != %q", id1, id2)
	}
	if id1 == "" {
		t.Fatal("install id should not be empty")
	}
	// The id must not embed the username or home path.
	if u := os.Getenv("USER"); u != "" && strings.Contains(id1, u) {
		t.Fatalf("install id leaked username: %q", id1)
	}
	if strings.Contains(id1, filepath.Base(home)) {
		t.Fatalf("install id leaked home path: %q", id1)
	}
}

func TestFlushQueuePreservesPendingEventsOnPartialFailure(t *testing.T) {
	home := t.TempDir()
	var attempts []int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		attempts = append(attempts, ev.InputBytes)
		mu.Unlock()

		if ev.InputBytes == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Second event fails
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Enqueue 5 events: 1, 2, 3, 4, 5
	for i := 1; i <= 5; i++ {
		enqueue(home, Event{
			Schema:             SchemaName,
			AnonymousInstallID: "test-install",
			InputBytes:         i,
			Adapter:            "codex",
			Surface:            "cli",
			Status:             "success",
		})
	}
	if len(readQueue(home)) != 5 {
		t.Fatalf("expected 5 queued events, got %d", len(readQueue(home)))
	}

	c := &Client{
		Home:       home,
		APIBase:    srv.URL,
		HTTPClient: srv.Client(),
	}

	c.flushQueue()

	remaining := readQueue(home)
	if len(remaining) != 4 {
		t.Fatalf("expected 4 remaining events in queue, got %d", len(remaining))
	}

	// The remaining events must be exactly 2, 3, 4, 5 in order.
	for idx, wantInput := range []int{2, 3, 4, 5} {
		if remaining[idx].InputBytes != wantInput {
			t.Fatalf("remaining[%d].InputBytes = %d, want %d", idx, remaining[idx].InputBytes, wantInput)
		}
	}
}

func TestTelemetryPermissionsHardened(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, ".xit")

	if err := SetEnabled(home, true); err != nil {
		t.Fatalf("SetEnabled failed: %v", err)
	}

	dirInfo, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat home failed: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("home directory permissions = %04o, want 0700", perm)
	}

	stateInfo, err := os.Stat(statePath(home))
	if err != nil {
		t.Fatalf("stat statePath failed: %v", err)
	}
	if perm := stateInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file permissions = %04o, want 0600", perm)
	}

	enqueue(home, Event{
		Schema:             SchemaName,
		Event:              "run.finished",
		AnonymousInstallID: "test-id",
		Adapter:            "claude",
		Surface:            "cli",
		Status:             "success",
	})

	queueInfo, err := os.Stat(queuePath(home))
	if err != nil {
		t.Fatalf("stat queuePath failed: %v", err)
	}
	if perm := queueInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("queue file permissions = %04o, want 0600", perm)
	}
}

func TestTelemetryRewritePreservesStrictPermissions(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, ".xit")

	// Pre-create directory with loose 0755
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(home, 0o755)

	// Pre-create state and queue files with loose 0644
	stPath := statePath(home)
	if err := os.WriteFile(stPath, []byte(`{"anonymous_install_id":"pre-existing"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(stPath, 0o644)

	qPath := queuePath(home)
	if err := os.WriteFile(qPath, []byte(`{"schema":"xit.metrics.v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(qPath, 0o644)

	// Rewrite state
	if err := SetEnabled(home, true); err != nil {
		t.Fatalf("SetEnabled failed: %v", err)
	}

	// Rewrite queue
	enqueue(home, Event{
		Schema:             SchemaName,
		Event:              "run.finished",
		AnonymousInstallID: "pre-existing",
		Adapter:            "codex",
		Surface:            "cli",
		Status:             "success",
	})

	// Verify directory permissions hardened to 0700
	if dirInfo, err := os.Stat(home); err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("home directory after rewrite = %04o, want 0700 (err: %v)", dirInfo.Mode().Perm(), err)
	}

	// Verify state file permissions hardened to 0600
	if stInfo, err := os.Stat(stPath); err != nil || stInfo.Mode().Perm() != 0o600 {
		t.Fatalf("state file after rewrite = %04o, want 0600 (err: %v)", stInfo.Mode().Perm(), err)
	}

	// Verify queue file permissions hardened to 0600
	if qInfo, err := os.Stat(qPath); err != nil || qInfo.Mode().Perm() != 0o600 {
		t.Fatalf("queue file after rewrite = %04o, want 0600 (err: %v)", qInfo.Mode().Perm(), err)
	}

	// Second rewrite (disable)
	if err := SetEnabled(home, false); err != nil {
		t.Fatalf("SetEnabled(false) failed: %v", err)
	}
	if stInfo, err := os.Stat(stPath); err != nil || stInfo.Mode().Perm() != 0o600 {
		t.Fatalf("state file after second rewrite = %04o, want 0600 (err: %v)", stInfo.Mode().Perm(), err)
	}
}
