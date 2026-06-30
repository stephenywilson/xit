// Package updatecheck implements XiT's version-check client.
//
// It fetches /v1/version from the XiT API, caches the result locally for 24h,
// and exposes a severity the CLI / VS Code extension can act on. Everything is
// fail-open: a network error, missing endpoint, or malformed response must never
// stop the user from running XiT.
//
// Severity ladder:
//
//	info        - no action; newer version merely exists
//	recommended - suggest upgrading
//	required    - strongly urge upgrade; high-risk paths (hooks / bridge) may refuse
//	blocked     - this version is below min_cli/min_vscode; block high-risk paths
//
// What is NEVER blocked, regardless of severity:
//
//	xit --version, xit doctor, xit upgrade, xit telemetry on|off|status
package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/apibase"
)

// cacheTTL bounds how often we hit the network. 24h per spec.
const cacheTTL = 24 * time.Hour

const fetchTimeout = 2 * time.Second

// Severity values, ordered.
const (
	SeverityInfo        = "info"
	SeverityRecommended = "recommended"
	SeverityRequired    = "required"
	SeverityBlocked     = "blocked"
)

// VersionInfo is the /v1/version response.
type VersionInfo struct {
	LatestCLI            string `json:"latest_cli"`
	MinCLI               string `json:"min_cli"`
	LatestVSCode         string `json:"latest_vscode"`
	MinVSCode            string `json:"min_vscode"`
	Severity             string `json:"severity"`
	Message              string `json:"message"`
	NpmCommand           string `json:"npm_command"`
	VSCodeMarketplaceURL string `json:"vscode_marketplace_url"`

	// FetchedAt is set by the client when caching; not part of the wire body.
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

// Result is what the CLI consumes after evaluating VersionInfo against the
// running version.
type Result struct {
	Info          VersionInfo
	CurrentCLI    string
	Available     bool   // a usable VersionInfo was obtained (network or cache)
	UpgradeNeeded bool   // current < latest_cli
	BelowMinimum  bool   // current < min_cli
	Severity      string // effective severity (may be escalated to blocked locally)
}

// ShouldBlockHighRisk reports whether high-risk paths (hooks / VS Code bridge)
// should refuse to run. Only required/blocked do so, and only when we actually
// have an answer. Fail-open: no info => never block.
func (r Result) ShouldBlockHighRisk() bool {
	if !r.Available {
		return false
	}
	return r.Severity == SeverityRequired || r.Severity == SeverityBlocked
}

// ShouldBlockCorePath reports whether XiT's CORE path (`xit auto`, hooks,
// VS Code bridge, high-noise routing) must be refused outright. ONLY the
// terminal "blocked" severity does this — info/recommended merely prompt, and
// required strongly urges an upgrade but still lets the command run. Like
// every other check it is fail-open: with no usable version info we never
// block, so a network failure can never lock a user out of `xit auto`.
//
// What this NEVER affects (guaranteed by their own dispatch, never reaching
// this gate): xit --version, xit doctor, xit upgrade, xit telemetry on/off/status.
func (r Result) ShouldBlockCorePath() bool {
	if !r.Available {
		return false
	}
	return r.Severity == SeverityBlocked
}

// UpgradeCommand returns the exact command a blocked user must run. Prefers the
// server-supplied npm_command, falling back to the canonical default. XiT never
// runs this automatically — it only tells the user.
func (r Result) UpgradeCommand() string {
	if cmd := strings.TrimSpace(r.Info.NpmCommand); cmd != "" {
		return cmd
	}
	return "npm install -g xitsg@latest"
}

// Client fetches and caches version info.
type Client struct {
	Home       string
	CurrentCLI string
	APIBase    string
	HTTPClient *http.Client
	now        func() time.Time
}

// NewClient builds a client. Home is the user XiT home (~/.xit).
func NewClient(home, currentCLI string) *Client {
	return &Client{
		Home:       home,
		CurrentCLI: currentCLI,
		APIBase:    apibase.Resolve(),
		HTTPClient: &http.Client{Timeout: fetchTimeout},
		now:        time.Now,
	}
}

func (c *Client) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// Check returns the evaluated result, using the 24h cache when fresh and the
// network otherwise. Always fail-open.
func (c *Client) Check() Result {
	info, ok := c.cached()
	if !ok {
		if fetched, err := c.fetch(); err == nil {
			fetched.FetchedAt = c.clock()
			c.writeCache(fetched)
			info, ok = fetched, true
		}
	}
	if !ok {
		// No network, no cache => fail open with an empty, non-blocking result.
		return Result{CurrentCLI: c.CurrentCLI, Available: false, Severity: SeverityInfo}
	}
	return c.evaluate(info)
}

// CheckLive always probes the network, bypassing the 24h cache. On success it
// refreshes the cache; on failure it returns an unavailable, non-blocking result
// rather than falling back to stale cache. This is for explicit diagnostics such
// as `xit update-check`, where reachability is the point of the command.
func (c *Client) CheckLive() Result {
	fetched, err := c.fetch()
	if err != nil {
		return Result{CurrentCLI: c.CurrentCLI, Available: false, Severity: SeverityInfo}
	}
	fetched.FetchedAt = c.clock()
	c.writeCache(fetched)
	return c.evaluate(fetched)
}

// CheckCachedOnly returns a result from cache only (never touches the network).
// High-frequency paths (every `xit auto`) use this so they stay fast/offline.
func (c *Client) CheckCachedOnly() Result {
	info, ok := c.cached()
	if !ok {
		return Result{CurrentCLI: c.CurrentCLI, Available: false, Severity: SeverityInfo}
	}
	return c.evaluate(info)
}

// RefreshAsync triggers a non-blocking background refresh if the cache is stale.
// Safe to call from hot paths; it never blocks and never errors out.
func (c *Client) RefreshAsync() {
	if _, fresh := c.cached(); fresh {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		if fetched, err := c.fetch(); err == nil {
			fetched.FetchedAt = c.clock()
			c.writeCache(fetched)
		}
	}()
}

func (c *Client) evaluate(info VersionInfo) Result {
	r := Result{Info: info, CurrentCLI: c.CurrentCLI, Available: true}
	r.UpgradeNeeded = info.LatestCLI != "" && Compare(c.CurrentCLI, info.LatestCLI) < 0
	r.BelowMinimum = info.MinCLI != "" && Compare(c.CurrentCLI, info.MinCLI) < 0

	sev := normalizeSeverity(info.Severity)
	// Local escalation: if we're below the declared minimum, treat as blocked
	// even if the server was conservative.
	if r.BelowMinimum && rank(sev) < rank(SeverityBlocked) {
		sev = SeverityBlocked
	}
	r.Severity = sev
	return r
}

func (c *Client) fetch() (VersionInfo, error) {
	var info VersionInfo
	if c.APIBase == "" {
		return info, errNoEndpoint
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/v1/version", nil)
	if err != nil {
		return info, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return info, errBadStatus
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, err
	}
	return info, nil
}

// --- cache --------------------------------------------------------------------

func cachePath(home string) string { return filepath.Join(home, "version-check.json") }

func (c *Client) cached() (VersionInfo, bool) {
	data, err := os.ReadFile(cachePath(c.Home))
	if err != nil {
		return VersionInfo{}, false
	}
	var info VersionInfo
	if json.Unmarshal(data, &info) != nil {
		return VersionInfo{}, false
	}
	if info.FetchedAt.IsZero() || c.clock().Sub(info.FetchedAt) > cacheTTL {
		return info, false // present but stale
	}
	return info, true
}

func (c *Client) writeCache(info VersionInfo) {
	if err := os.MkdirAll(c.Home, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(cachePath(c.Home), append(data, '\n'), 0o644)
}

// --- helpers ------------------------------------------------------------------

type ucError string

func (e ucError) Error() string { return string(e) }

const (
	errNoEndpoint ucError = "no XIT_API_BASE configured"
	errBadStatus  ucError = "version endpoint returned non-2xx"
)

func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case SeverityRecommended:
		return SeverityRecommended
	case SeverityRequired:
		return SeverityRequired
	case SeverityBlocked:
		return SeverityBlocked
	default:
		return SeverityInfo
	}
}

func rank(s string) int {
	switch s {
	case SeverityRecommended:
		return 1
	case SeverityRequired:
		return 2
	case SeverityBlocked:
		return 3
	default:
		return 0
	}
}

// Compare compares two dotted version strings ("0.2.48"). Returns -1, 0, or 1.
// Non-numeric / missing segments are treated as 0. Robust to a leading "v".
func Compare(a, b string) int {
	as := splitVersion(a)
	bs := splitVersion(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(as) {
			ai = as[i]
		}
		if i < len(bs) {
			bi = bs[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func splitVersion(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		// tolerate pre-release suffixes like "1-rc1"
		if idx := strings.IndexAny(p, "-+"); idx >= 0 {
			p = p[:idx]
		}
		n, _ := strconv.Atoi(strings.TrimSpace(p))
		out = append(out, n)
	}
	return out
}
