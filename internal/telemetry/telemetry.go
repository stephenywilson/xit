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

// NewClient resolves config. home is the user XiT home (~/.xit).
// It does not create an install id on disk if telemetry is disabled.
func NewClient(home, cliVersion string) *Client {
	st, _ := loadState(home)
	return &Client{
		Home:               home,
		CLIVersion:         cliVersion,
		APIBase:            resolveAPIBase(),
		HTTPClient:         &http.Client{Timeout: sendTimeout},
		now:                time.Now,
		anonymousInstallID: st.AnonymousInstallID,
	}
}

func (c *Client) getOrEnsureInstallID() string {
	if c.anonymousInstallID != "" {
		return c.anonymousInstallID
	}
	c.anonymousInstallID = ensureInstallID(c.Home)
	return c.anonymousInstallID
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
		AnonymousInstallID:     c.getOrEnsureInstallID(),
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
	for i, ev := range pending {
		if !c.postOne(ev) {
			// re-spool the rest (failed event and all subsequent events) and stop; try again next time.
			for _, rem := range pending[i:] {
				enqueue(c.Home, rem)
			}
			return
		}
	}
}

// --- enable/disable resolution -------------------------------------------------

const CommunityConsentVersion = 1

// Enabled resolves whether telemetry should run. Priority (highest first):
//  1. XIT_TELEMETRY=off / on (env always wins)
//  2. DO_NOT_TRACK=1 (industry convention => disabled, unless env explicitly on)
//  3. versioned community consent in the local state file (~/.xit/telemetry.json)
//  4. default: disabled — telemetry is strictly opt-in
func Enabled(home string) bool {
	enabled, _ := resolveEnabled(home)
	return enabled
}

// EnabledSource explains *why* telemetry is on/off, for `xit telemetry status`.
func EnabledSource(home string) (enabled bool, source string) {
	return resolveEnabled(home)
}

func resolveEnabled(home string) (bool, string) {
	if enabled, source, ok := parseXITTelemetryEnv(os.Getenv("XIT_TELEMETRY")); ok {
		return enabled, source
	}
	if doNotTrackEnabled(os.Getenv("DO_NOT_TRACK")) {
		return false, "DO_NOT_TRACK=1"
	}
	st, err := LoadState(home)
	if err == nil && st.hasVersionedCommunityConsent() {
		return true, "versioned local consent (~/.xit/telemetry.json)"
	}
	if err == nil {
		if st.hasLegacyEnabled {
			return false, "legacy config requires confirmation"
		}
		return false, "local telemetry config off (~/.xit/telemetry.json)"
	}
	return false, "default off"
}

func parseXITTelemetryEnv(value string) (enabled bool, source string, ok bool) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "off", "0", "false", "no", "disable", "disabled":
		return false, "XIT_TELEMETRY=" + v, true
	case "on", "1", "true", "yes", "enable", "enabled":
		return true, "XIT_TELEMETRY=" + v, true
	default:
		return false, "", false
	}
}

func doNotTrackEnabled(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "1" || v == "true"
}

// SetEnabled persists the on/off choice to the local state file.
func SetEnabled(home string, enabled bool) error {
	st, _ := LoadState(home)
	if st.AnonymousInstallID == "" && enabled {
		st.AnonymousInstallID = newInstallID()
	}
	st.CommunityStatisticsEnabled = enabled
	if enabled {
		st.ConsentVersion = CommunityConsentVersion
		now := time.Now().UTC().Format(time.RFC3339)
		st.ConsentedAt = &now
	}
	return SaveState(home, st)
}

// InstallID returns the anonymous install id (creating one if needed).
func InstallID(home string) string { return ensureInstallID(home) }

// CurrentInstallID returns the existing install id without mutating disk.
// If telemetry has never been enabled or no ID has been created, it returns "".
func CurrentInstallID(home string) string {
	st, err := LoadState(home)
	if err == nil && st.AnonymousInstallID != "" {
		return st.AnonymousInstallID
	}
	return ""
}

// HasPendingQueue checks whether any events are queued locally.
func HasPendingQueue(home string) bool {
	return len(readQueue(home)) > 0
}

// --- local state file ----------------------------------------------------------

type State struct {
	AnonymousInstallID         string  `json:"anonymous_install_id"`
	CommunityStatisticsEnabled bool    `json:"community_statistics_enabled"`
	ConsentVersion             int     `json:"consent_version"`
	ConsentedAt                *string `json:"consented_at,omitempty"`

	hasLegacyEnabled bool
}

type stateOnDisk struct {
	AnonymousInstallID         string  `json:"anonymous_install_id"`
	LegacyEnabled              *bool   `json:"enabled,omitempty"`
	CommunityStatisticsEnabled bool    `json:"community_statistics_enabled"`
	ConsentVersion             int     `json:"consent_version"`
	ConsentedAt                *string `json:"consented_at,omitempty"`
}

func statePath(home string) string { return filepath.Join(home, "telemetry.json") }

func (s State) hasVersionedCommunityConsent() bool {
	return s.CommunityStatisticsEnabled && s.ConsentVersion == CommunityConsentVersion
}

func LoadState(home string) (State, error) {
	return loadState(home)
}

func loadState(home string) (State, error) {
	var disk stateOnDisk
	data, err := os.ReadFile(statePath(home))
	if err != nil {
		return State{}, err
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		return State{}, err
	}
	return State{
		AnonymousInstallID:         disk.AnonymousInstallID,
		CommunityStatisticsEnabled: disk.CommunityStatisticsEnabled,
		ConsentVersion:             disk.ConsentVersion,
		ConsentedAt:                disk.ConsentedAt,
		hasLegacyEnabled:           disk.LegacyEnabled != nil,
	}, nil
}

func SaveState(home string, s State) error {
	return saveState(home, s)
}

func saveState(home string, s State) error {
	if err := ensureSecureDir(home); err != nil {
		return err
	}
	out := stateOnDisk{
		AnonymousInstallID:         s.AnonymousInstallID,
		CommunityStatisticsEnabled: s.CommunityStatisticsEnabled,
		ConsentVersion:             s.ConsentVersion,
		ConsentedAt:                s.ConsentedAt,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return writeSecureFile(statePath(home), append(data, '\n'))
}

func ensureSecureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func writeSecureFile(path string, data []byte) error {
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmpPath, 0o600)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Chmod(path, 0o600)
}

func ensureInstallID(home string) string {
	st, err := loadState(home)
	if err == nil && st.AnonymousInstallID != "" {
		return st.AnonymousInstallID
	}
	st.AnonymousInstallID = newInstallID()
	_ = saveState(home, st)
	return st.AnonymousInstallID
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
	if err := ensureSecureDir(home); err != nil {
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
	_ = writeSecureFile(queuePath(home), b.Bytes())
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
