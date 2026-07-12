// Package telemetry implements XiT's anonymous usage metrics.
//
// Design constraints (see docs/telemetry.md):
//   - Anonymous: a single random install id, never a username / path / repo / full session id.
//   - Transparent: documented, and easy to disable (`xit telemetry off` / XIT_TELEMETRY=off).
//   - Privacy-first: the Event struct below is the *only* shape ever sent. It has no
//     field for raw output, prompts, AI replies, command text, cwd, file paths, or secrets.
//     There is intentionally no way to attach those — the wire schema cannot carry them.
//   - Fail-open: building, queuing and sending must never break `xit auto`.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/apibase"
)

// SchemaName is the wire schema identifier the backend validates against.
const SchemaName = "xit.metrics.v1"

// maxQueue caps the local spool so a long offline streak can never grow unbounded.
const maxQueue = 100

// sendTimeout bounds a single delivery attempt. Telemetry must never stall a run.
const sendTimeout = 1 * time.Second

// Event is the complete, exhaustive set of fields XiT may transmit.
//
// IMPORTANT: do not add command, cwd, path, prompt, raw output, token, secret,
// username, email, repo, or full-session-id fields here. The privacy guarantee
// is enforced structurally by this struct (and asserted in telemetry_test.go).
type Event struct {
	Schema                 string  `json:"schema"`
	Event                  string  `json:"event"`
	AnonymousInstallID     string  `json:"anonymous_install_id"`
	Timestamp              string  `json:"timestamp"`
	CLIVersion             string  `json:"cli_version"`
	VSCodeExtensionVersion string  `json:"vscode_extension_version,omitempty"`
	Adapter                string  `json:"adapter"`
	Surface                string  `json:"surface"`
	OS                     string  `json:"os"`
	Arch                   string  `json:"arch"`
	InputBytes             int     `json:"input_bytes"`
	SummaryBytes           int     `json:"summary_bytes"`
	SavedBytes             int     `json:"saved_bytes"`
	EstimatedSavedTokens   int     `json:"estimated_saved_tokens"`
	CompressionRatio       float64 `json:"compression_ratio"`
	RunCount               int     `json:"run_count"`
	Status                 string  `json:"status"`
	ErrorKind              string  `json:"error_kind"`
}

// allowedAdapters is the closed set of adapter labels. Anything else collapses
// to "unknown" so we never leak a custom/derived value.
var allowedAdapters = map[string]bool{
	"codex": true, "claude": true, "kimi": true,
	"opencode": true, "cursor": true, "vscode": true, "unknown": true,
}

// codex_cli / codex_ide / chatgpt_desktop_codex / codex_shared are the
// finer-grained Codex front-end breakdown (see internal/codexhook.DetectSurface).
// adapter stays "codex" for all of them; only surface distinguishes the
// front-end, so historical Codex data stays continuous under one adapter.
var allowedSurfaces = map[string]bool{
	"cli": true, "hook": true, "vscode": true, "bridge": true,
	"codex_cli": true, "codex_ide": true, "chatgpt_desktop_codex": true, "codex_shared": true,
}

// Metrics is the privacy-safe input a caller assembles. Note there is no command,
// cwd or output field here either — callers physically cannot pass them through.
type Metrics struct {
	Event        string
	Adapter      string
	Surface      string
	InputBytes   int
	SummaryBytes int
	SavedBytes   int
	RunCount     int
	Status       string // "success" | "error"
	ErrorKind    string // "none" | "timeout" | "command_failed" | "parse_failed" | "unknown"
}

// EstimatedSavedTokens uses XiT's standard saved_bytes/4 estimate.
func EstimatedSavedTokens(savedBytes int) int {
	if savedBytes <= 0 {
		return 0
	}
	return savedBytes / 4
}

// CompressionRatio is saved_bytes / input_bytes, clamped to [0,1].
func CompressionRatio(inputBytes, savedBytes int) float64 {
	if inputBytes <= 0 || savedBytes <= 0 {
		return 0
	}
	r := float64(savedBytes) / float64(inputBytes)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

func normalizeAdapter(a string) string {
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" || !allowedAdapters[a] {
		return "unknown"
	}
	return a
}

func normalizeSurface(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !allowedSurfaces[s] {
		return "cli"
	}
	return s
}

func normalizeStatus(s string) string {
	if s == "error" {
		return "error"
	}
	return "success"
}

func normalizeErrorKind(k string) string {
	switch k {
	case "timeout", "command_failed", "parse_failed", "unknown":
		return k
	default:
		return "none"
	}
}

// Client carries the resolved configuration for an emit. Construct via NewClient.
type Client struct {
	Home               string
	CLIVersion         string
	VSCodeVersion      string
	APIBase            string // empty => sending is a no-op (fail-open)
	HTTPClient         *http.Client
	now                func() time.Time
	anonymousInstallID string
}

// NewClient resolves config + install id. home is the user XiT home (~/.xit).
func NewClient(home, cliVersion string) *Client {
	return &Client{
		Home:               home,
		CLIVersion:         cliVersion,
		APIBase:            resolveAPIBase(),
		HTTPClient:         &http.Client{Timeout: sendTimeout},
		now:                time.Now,
		anonymousInstallID: ensureInstallID(home),
	}
}

// resolveAPIBase reads the backend base URL: XIT_API_BASE overrides the
// built-in production default (apibase.Default). Both empty => telemetry
// silently no-ops (fail-open), never hammering a placeholder domain.
func resolveAPIBase() string {
	return apibase.Resolve()
}

// Build assembles a fully-normalized Event from privacy-safe Metrics. It can
// never carry a disallowed field because Metrics has none.
func (c *Client) Build(m Metrics) Event {
	now := c.now
	if now == nil {
		now = time.Now
	}
	saved := m.SavedBytes
	if saved < 0 {
		saved = 0
	}
	return Event{
		Schema:                 SchemaName,
		Event:                  emptyTo(m.Event, "run.finished"),
		AnonymousInstallID:     c.anonymousInstallID,
		Timestamp:              now().UTC().Format(time.RFC3339),
		CLIVersion:             c.CLIVersion,
		VSCodeExtensionVersion: c.VSCodeVersion,
		Adapter:                normalizeAdapter(m.Adapter),
		Surface:                normalizeSurface(m.Surface),
		OS:                     osLabel(),
		Arch:                   archLabel(),
		InputBytes:             m.InputBytes,
		SummaryBytes:           m.SummaryBytes,
		SavedBytes:             saved,
		EstimatedSavedTokens:   EstimatedSavedTokens(saved),
		CompressionRatio:       CompressionRatio(m.InputBytes, saved),
		RunCount:               maxInt(m.RunCount, 0),
		Status:                 normalizeStatus(m.Status),
		ErrorKind:              normalizeErrorKind(m.ErrorKind),
	}
}

// Emit builds + (if enabled) asynchronously delivers an event. It returns
// immediately and never blocks the caller; failures are silent (fail-open).
func (c *Client) Emit(m Metrics) {
	if !Enabled(c.Home) {
		return
	}
	ev := c.Build(m)
	go c.deliver(ev)
}

// EmitSync builds + delivers inline, bounded by the per-attempt timeout. It is
// for short-lived CLI processes that are about to exit (where a detached
// goroutine would be killed before it could send/spool). When no endpoint is
// configured it returns instantly; otherwise it adds at most ~1s, and only
// after the user's command has already completed and printed its output.
func (c *Client) EmitSync(m Metrics) {
	if !Enabled(c.Home) {
		return
	}
	c.deliver(c.Build(m))
}

// deliver tries the live endpoint, then spools to the local queue on failure.
// Spooled events are flushed (best-effort) on the next successful delivery.
func (c *Client) deliver(ev Event) {
	defer func() { _ = recover() }() // never let telemetry panic a run
	if c.APIBase == "" {
		return // no endpoint configured => nothing to do, by design
	}
	if c.postOne(ev) {
		c.flushQueue()
		return
	}
	enqueue(c.Home, ev)
}

func (c *Client) postOne(ev Event) bool {
	body, err := json.Marshal(ev)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+"/v1/metrics", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (c *Client) flushQueue() {
	pending := drainQueue(c.Home)
	for _, ev := range pending {
		if !c.postOne(ev) {
			// re-spool the rest and stop; try again next time.
			enqueue(c.Home, ev)
			return
		}
	}
}

// --- enable/disable resolution -------------------------------------------------

// Enabled resolves whether telemetry should run. Priority (highest first):
//  1. XIT_TELEMETRY=off / on (env always wins)
//  2. DO_NOT_TRACK=1 (industry convention => disabled, unless env explicitly on)
//  3. local state file (~/.xit/telemetry.json)
//  4. default: enabled (true) — anonymous-by-default, easy to disable
func Enabled(home string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XIT_TELEMETRY"))) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false
	case "on", "1", "true", "yes", "enable", "enabled":
		return true
	}
	if v := strings.TrimSpace(os.Getenv("DO_NOT_TRACK")); v == "1" || strings.EqualFold(v, "true") {
		return false
	}
	st, err := loadState(home)
	if err == nil && st.set {
		return st.Enabled
	}
	return true
}

// EnabledSource explains *why* telemetry is on/off, for `xit telemetry status`.
func EnabledSource(home string) (enabled bool, source string) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XIT_TELEMETRY"))) {
	case "off", "0", "false", "no", "disable", "disabled":
		return false, "XIT_TELEMETRY env"
	case "on", "1", "true", "yes", "enable", "enabled":
		return true, "XIT_TELEMETRY env"
	}
	if v := strings.TrimSpace(os.Getenv("DO_NOT_TRACK")); v == "1" || strings.EqualFold(v, "true") {
		return false, "DO_NOT_TRACK env"
	}
	st, err := loadState(home)
	if err == nil && st.set {
		return st.Enabled, "config (~/.xit/telemetry.json)"
	}
	return true, "default (anonymous metrics on)"
}

// SetEnabled persists the on/off choice to the local state file.
func SetEnabled(home string, enabled bool) error {
	st, _ := loadState(home)
	st.Enabled = enabled
	st.set = true
	if st.InstallID == "" {
		st.InstallID = newInstallID()
	}
	return saveState(home, st)
}

// InstallID returns the anonymous install id (creating one if needed).
func InstallID(home string) string { return ensureInstallID(home) }

// --- local state file ----------------------------------------------------------

type state struct {
	InstallID string `json:"anonymous_install_id"`
	Enabled   bool   `json:"enabled"`
	set       bool   // whether "enabled" was present in the file
}

func statePath(home string) string { return filepath.Join(home, "telemetry.json") }

func loadState(home string) (state, error) {
	var s state
	data, err := os.ReadFile(statePath(home))
	if err != nil {
		return s, err
	}
	// detect presence of "enabled" before unmarshalling into the typed struct.
	var probe map[string]json.RawMessage
	if json.Unmarshal(data, &probe) == nil {
		_, s.set = probe["enabled"]
	}
	_ = json.Unmarshal(data, &s)
	return s, nil
}

func saveState(home string, s state) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	out := struct {
		InstallID string `json:"anonymous_install_id"`
		Enabled   bool   `json:"enabled"`
	}{InstallID: s.InstallID, Enabled: s.Enabled}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(home), append(data, '\n'), 0o644)
}

func ensureInstallID(home string) string {
	st, err := loadState(home)
	if err == nil && st.InstallID != "" {
		return st.InstallID
	}
	st.InstallID = newInstallID()
	if !st.set {
		st.Enabled = true // default-on; record the id alongside
	}
	_ = saveState(home, st)
	return st.InstallID
}

func newInstallID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "xit-" + time.Now().UTC().Format("20060102150405")
	}
	return hex.EncodeToString(b)
}

// --- local spool ---------------------------------------------------------------

func queuePath(home string) string { return filepath.Join(home, "telemetry-queue.jsonl") }

func enqueue(home string, ev Event) {
	events := readQueue(home)
	events = append(events, ev)
	if len(events) > maxQueue {
		events = events[len(events)-maxQueue:] // keep newest maxQueue
	}
	writeQueue(home, events)
}

func drainQueue(home string) []Event {
	events := readQueue(home)
	_ = os.Remove(queuePath(home))
	return events
}

func readQueue(home string) []Event {
	data, err := os.ReadFile(queuePath(home))
	if err != nil {
		return nil
	}
	var out []Event
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(line), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}

func writeQueue(home string, events []Event) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return
	}
	var b bytes.Buffer
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	_ = os.WriteFile(queuePath(home), b.Bytes(), 0o644)
}

// --- small helpers --------------------------------------------------------------

func emptyTo(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
