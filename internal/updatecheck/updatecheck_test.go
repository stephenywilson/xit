package updatecheck

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.48", "0.2.49", -1},
		{"0.2.49", "0.2.48", 1},
		{"0.2.48", "0.2.48", 0},
		{"v0.2.48", "0.2.48", 0},
		{"0.2.9", "0.2.10", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.2.49-rc1", "0.2.49", 0},
	}
	for _, tc := range cases {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Fatalf("Compare(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestFailOpenNoNetworkNoCache(t *testing.T) {
	c := NewClient(t.TempDir(), "0.2.48")
	c.APIBase = "" // no endpoint
	r := c.Check()
	if r.Available {
		t.Fatal("expected unavailable result with no endpoint/cache")
	}
	if r.ShouldBlockHighRisk() {
		t.Fatal("fail-open: must never block when no version info is available")
	}
	if r.Severity != SeverityInfo {
		t.Fatalf("severity should default to info, got %q", r.Severity)
	}
}

func TestFailOpenOnNetworkError(t *testing.T) {
	c := NewClient(t.TempDir(), "0.2.48")
	c.APIBase = "http://127.0.0.1:0" // unroutable
	r := c.Check()
	if r.Available || r.ShouldBlockHighRisk() {
		t.Fatal("network error must fail-open (unavailable, non-blocking)")
	}
}

func TestRequiredSeverityReturnsClearState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"latest_cli":"0.2.49","min_cli":"0.2.48",
			"severity":"required","message":"Please upgrade XiT.",
			"npm_command":"npm install -g xitsg@latest"
		}`))
	}))
	defer srv.Close()

	c := NewClient(t.TempDir(), "0.2.48")
	c.APIBase = srv.URL
	r := c.Check()
	if !r.Available {
		t.Fatal("expected available result")
	}
	if r.Severity != SeverityRequired {
		t.Fatalf("severity want required, got %q", r.Severity)
	}
	// 0.2.51 follow-up: "required" is purely advisory — only "blocked" (current
	// < min_cli) may disable high-risk paths. current(0.2.48) == min_cli(0.2.48)
	// here, so this must NOT block anything.
	if r.ShouldBlockHighRisk() {
		t.Fatal("required severity must not block high-risk paths (only 'blocked' may)")
	}
	if r.Info.Message != "Please upgrade XiT." {
		t.Fatalf("message not surfaced: %q", r.Info.Message)
	}
}

func TestBelowMinimumEscalatesToBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server only says "info", but current(0.2.40) < min_cli(0.2.48).
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.49","min_cli":"0.2.48","severity":"info"}`))
	}))
	defer srv.Close()

	c := NewClient(t.TempDir(), "0.2.40")
	c.APIBase = srv.URL
	r := c.Check()
	if !r.BelowMinimum {
		t.Fatal("0.2.40 should be below min 0.2.48")
	}
	if r.Severity != SeverityBlocked {
		t.Fatalf("below-minimum should escalate to blocked, got %q", r.Severity)
	}
	if !r.ShouldBlockHighRisk() {
		t.Fatal("blocked severity should block high-risk paths")
	}
}

func TestInfoSeverityDoesNotBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.49","min_cli":"0.2.40","severity":"recommended"}`))
	}))
	defer srv.Close()

	c := NewClient(t.TempDir(), "0.2.48")
	c.APIBase = srv.URL
	r := c.Check()
	if r.ShouldBlockHighRisk() {
		t.Fatal("recommended severity must not block")
	}
	if !r.UpgradeNeeded {
		t.Fatal("0.2.48 < 0.2.49 should flag upgrade needed")
	}
	if r.BelowMinimum {
		t.Fatal("0.2.48 >= min 0.2.40 should not be below minimum")
	}
}

// TestShouldBlockCorePathOnlyForBlocked locks in the corrected version-gate
// policy (spec §六): ONLY current < min_cli refuses the core `xit auto` path.
// info/recommended/required must never block the command itself, and neither
// does a server-declared severity=blocked when current is at or above min_cli
// — current=0.2.48 here is ABOVE min_cli=0.2.40 in every case, so none of
// these may block, regardless of what `severity` the server sent.
func TestShouldBlockCorePathOnlyForBlocked(t *testing.T) {
	cases := []struct {
		severity string
		// current/min chosen so severity isn't escalated by BelowMinimum.
		body  string
		block bool
	}{
		{"info", `{"latest_cli":"0.2.49","min_cli":"0.2.40","severity":"info"}`, false},
		{"recommended", `{"latest_cli":"0.2.49","min_cli":"0.2.40","severity":"recommended"}`, false},
		{"required", `{"latest_cli":"0.2.49","min_cli":"0.2.40","severity":"required"}`, false},
		// current (0.2.48) >= min_cli (0.2.40): a server-declared "blocked" must
		// be downgraded and NEVER refuse the core path — this is the exact bug
		// class fixed in 0.2.51 (see TestEqualToMinimumIsNeverBlocked below).
		{"blocked-but-above-min", `{"latest_cli":"0.2.49","min_cli":"0.2.40","severity":"blocked"}`, false},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(tc.body))
		}))
		c := NewClient(t.TempDir(), "0.2.48")
		c.APIBase = srv.URL
		r := c.Check()
		if got := r.ShouldBlockCorePath(); got != tc.block {
			t.Fatalf("severity=%s: ShouldBlockCorePath()=%v want %v", tc.severity, got, tc.block)
		}
		srv.Close()
	}
}

// TestVersionGateMatrix locks in the exact CLI current/min/latest matrix from
// the 0.2.51 spec. This is the regression test for the 0.2.50 self-block bug:
// production briefly served min_cli=0.2.50 with severity=blocked while
// latest_cli was also 0.2.50, and the OLD evaluate() blindly trusted the
// server's "blocked" flag even though current == min_cli (never below it).
func TestVersionGateMatrix(t *testing.T) {
	cases := []struct {
		name      string
		current   string
		latestCLI string
		minCLI    string
		// serverSeverity is what a (possibly overly-conservative) server sends.
		serverSeverity string
		wantBlocked    bool
	}{
		{"below minimum is blocked", "0.2.48", "0.2.51", "0.2.49", "info", true},
		{"equal to minimum is allowed", "0.2.49", "0.2.49", "0.2.49", "blocked", false},
		{"above minimum is allowed", "0.2.50", "0.2.51", "0.2.49", "blocked", false},
		{"exactly the historical incident: current==min==latest", "0.2.50", "0.2.50", "0.2.50", "blocked", false},
		{"new release above minimum is allowed", "0.2.51", "0.2.51", "0.2.50", "info", false},
		{"update available but not below minimum stays allowed", "0.2.51", "0.2.52", "0.2.50", "recommended", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"latest_cli":"` + tc.latestCLI + `","min_cli":"` + tc.minCLI + `","severity":"` + tc.serverSeverity + `"}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c := NewClient(t.TempDir(), tc.current)
			c.APIBase = srv.URL
			r := c.Check()
			if got := r.ShouldBlockCorePath(); got != tc.wantBlocked {
				t.Fatalf("current=%s min=%s latest=%s server_severity=%s: ShouldBlockCorePath()=%v want %v",
					tc.current, tc.minCLI, tc.latestCLI, tc.serverSeverity, got, tc.wantBlocked)
			}
		})
	}
}

// TestEqualToMinimumIsNeverBlocked is the precise regression test for the
// production incident: min_cli=0.2.50, current=0.2.50, server severity=blocked.
func TestEqualToMinimumIsNeverBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.50","min_cli":"0.2.50","severity":"blocked","message":"A critical XiT update is required."}`))
	}))
	defer srv.Close()

	c := NewClient(t.TempDir(), "0.2.50")
	c.APIBase = srv.URL
	r := c.Check()
	if r.BelowMinimum {
		t.Fatal("0.2.50 == min_cli 0.2.50 must not be BelowMinimum (equality is not below)")
	}
	if r.ShouldBlockCorePath() {
		t.Fatal("current == min_cli must never block the core path, even if the server says severity=blocked")
	}
	if r.ShouldBlockHighRisk() {
		t.Fatal("current == min_cli must never block high-risk paths either")
	}
	// current (0.2.50) == latest_cli (0.2.50) too in this fixture, so
	// UpgradeNeeded is false and severity must be "info" — not merely
	// downgraded to "required" — regardless of the server's "blocked" flag.
	if r.Severity != SeverityInfo {
		t.Fatalf("current == min_cli == latest_cli with server severity=blocked should evaluate to info, got %q", r.Severity)
	}
}

// TestSemverOrderingNotLexicographic guards against a naive string/lexical
// compare, which would incorrectly rank "0.9.9" above "0.10.0".
func TestSemverOrderingNotLexicographic(t *testing.T) {
	if got := Compare("0.10.0", "0.9.9"); got != 1 {
		t.Fatalf("Compare(0.10.0, 0.9.9)=%d want 1 (semver, not lexicographic)", got)
	}
	if got := Compare("0.9.9", "0.10.0"); got != -1 {
		t.Fatalf("Compare(0.9.9, 0.10.0)=%d want -1", got)
	}
}

// TestMalformedVersionIsSafe: a malformed min_cli/latest_cli from the server
// must never crash and must never cause a well-formed current version to be
// wrongly treated as below minimum (fail-safe: garbage input never blocks).
func TestMalformedVersionIsSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"latest_cli":"not-a-version","min_cli":"also garbage","severity":"blocked"}`))
	}))
	defer srv.Close()

	c := NewClient(t.TempDir(), "0.2.50")
	c.APIBase = srv.URL
	r := c.Check() // must not panic
	if r.BelowMinimum {
		t.Fatal("malformed min_cli must never cause a well-formed current version to appear below minimum")
	}
	if r.ShouldBlockCorePath() {
		t.Fatal("malformed version data must never block the core path")
	}
}

// TestExplicitUpdateCheckRefreshesPastStaleBlockedCache proves that an
// explicit `xit update-check` (CheckLive) refreshes the cache and returns the
// fresh result — a stale cached "blocked" entry can never linger and keep
// overriding what the server now actually reports.
func TestExplicitUpdateCheckRefreshesPastStaleBlockedCache(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.51","min_cli":"0.2.51","severity":"info"}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	c := NewClient(home, "0.2.51")
	c.APIBase = srv.URL
	base := time.Now()
	c.now = func() time.Time { return base }
	// Seed the cache with a stale, incorrectly-blocked entry (simulating the
	// production incident's cached response), fresh enough to normally win.
	c.writeCache(VersionInfo{
		LatestCLI: "0.2.50",
		MinCLI:    "0.2.50",
		Severity:  SeverityBlocked,
		FetchedAt: base,
	})

	// A plain Check() would still use the fresh-looking cache and (pre-fix)
	// could stay blocked; confirm the fix means even the cached path is safe.
	if r := c.Check(); r.ShouldBlockCorePath() {
		t.Fatal("cached blocked severity at current==min_cli must not block (fixed at evaluate(), not just via refetch)")
	}

	// Now force an explicit live refresh — it must hit the network and the
	// cache file on disk must reflect the fresh (non-blocked) data afterward.
	r := c.CheckLive()
	if hits != 1 {
		t.Fatalf("CheckLive must hit the network, got %d hits", hits)
	}
	if r.ShouldBlockCorePath() {
		t.Fatal("live-refreshed result must not be blocked")
	}
	cachedAfter, ok := c.cached()
	if !ok || cachedAfter.Severity != SeverityInfo {
		t.Fatalf("cache on disk must reflect the fresh response, got %+v ok=%v", cachedAfter, ok)
	}
}

// TestCorePathFailOpen: with no usable version info (network/cache failure),
// the core path must NEVER be blocked — a backend outage can't lock a user
// out of `xit auto`.
func TestCorePathFailOpen(t *testing.T) {
	c := NewClient(t.TempDir(), "0.2.48")
	c.APIBase = "" // no endpoint, no cache
	r := c.Check()
	if r.ShouldBlockCorePath() {
		t.Fatal("fail-open: core path must not be blocked without version info")
	}

	c2 := NewClient(t.TempDir(), "0.2.48")
	c2.APIBase = "http://127.0.0.1:0" // unroutable
	if c2.Check().ShouldBlockCorePath() {
		t.Fatal("network error must fail-open for the core path too")
	}
}

func TestUpgradeCommandPrefersServerThenDefault(t *testing.T) {
	r := Result{Available: true, Info: VersionInfo{NpmCommand: "npm install -g xitsg@1.2.3"}}
	if got := r.UpgradeCommand(); got != "npm install -g xitsg@1.2.3" {
		t.Fatalf("should use server npm_command, got %q", got)
	}
	r2 := Result{Available: true}
	if got := r2.UpgradeCommand(); got != "npm install -g xitsg@latest" {
		t.Fatalf("should fall back to canonical default, got %q", got)
	}
}

func TestCacheUsedWithin24h(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.49","min_cli":"0.2.48","severity":"info"}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	c := NewClient(home, "0.2.49")
	c.APIBase = srv.URL
	c.Check()
	c.Check()
	if hits != 1 {
		t.Fatalf("second Check within 24h should hit cache, server hits=%d", hits)
	}
}

func TestCheckLiveBypassesFreshCache(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.50","min_cli":"0.2.48","severity":"recommended"}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	c := NewClient(home, "0.2.49")
	c.APIBase = srv.URL
	base := time.Now()
	c.now = func() time.Time { return base }
	c.writeCache(VersionInfo{
		LatestCLI: "0.2.49",
		MinCLI:    "0.2.48",
		Severity:  SeverityInfo,
		FetchedAt: base,
	})

	r := c.CheckLive()
	if hits != 1 {
		t.Fatalf("CheckLive should hit network despite fresh cache, server hits=%d", hits)
	}
	if !r.Available || r.Info.LatestCLI != "0.2.50" {
		t.Fatalf("CheckLive should return live response, got available=%v latest=%q", r.Available, r.Info.LatestCLI)
	}
}

func TestStaleCacheRefetches(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"latest_cli":"0.2.49","min_cli":"0.2.48","severity":"info"}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	c := NewClient(home, "0.2.49")
	c.APIBase = srv.URL
	base := time.Now()
	c.now = func() time.Time { return base }
	c.Check()
	// Jump 25h forward => cache stale.
	c.now = func() time.Time { return base.Add(25 * time.Hour) }
	c.Check()
	if hits != 2 {
		t.Fatalf("stale cache should refetch, server hits=%d", hits)
	}
}

// TestCurrentAheadOfLatestIsNeverRequiredOrBlocked is the regression test for
// the second production incident: current > latest_cli (or == latest_cli)
// must always be "info"/up-to-date and must never keep a server-declared
// "required" or "blocked" severity alive, regardless of min_cli. A stale
// server that hasn't caught up to a newly-published CLI build can never
// disable the VS Code bridge or claim an upgrade is needed for a version
// that is already at or ahead of latest_cli.
func TestCurrentAheadOfLatestIsNeverRequiredOrBlocked(t *testing.T) {
	cases := []struct {
		name           string
		current        string
		minCLI         string
		latestCLI      string
		serverSeverity string
	}{
		{"current == latest, server says required", "0.2.50", "0.2.50", "0.2.50", "required"},
		{"current == latest, server says blocked", "0.2.50", "0.2.50", "0.2.50", "blocked"},
		{"current > latest, server says required (the exact reported bug)", "0.2.51", "0.2.50", "0.2.50", "required"},
		{"current > latest, server says blocked", "0.2.51", "0.2.50", "0.2.50", "blocked"},
		{"current == min == latest, server says blocked", "0.2.51", "0.2.51", "0.2.51", "blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"latest_cli":"` + tc.latestCLI + `","min_cli":"` + tc.minCLI + `","severity":"` + tc.serverSeverity + `"}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c := NewClient(t.TempDir(), tc.current)
			c.APIBase = srv.URL
			r := c.Check()

			if r.Severity != SeverityInfo {
				t.Fatalf("severity = %q, want %q (current >= latest_cli must never stay %q)", r.Severity, SeverityInfo, tc.serverSeverity)
			}
			if r.UpgradeNeeded {
				t.Fatal("UpgradeNeeded must be false when current >= latest_cli")
			}
			if r.ShouldBlockCorePath() {
				t.Fatal("core path (`xit auto`) must never be blocked when current >= latest_cli")
			}
			if r.ShouldBlockHighRisk() {
				t.Fatal("high-risk paths (VS Code bridge) must never be refused when current >= latest_cli")
			}
		})
	}
}

// TestFullVersionGateMatrixV2 is the complete current/min/latest matrix from
// the 0.2.51 follow-up spec, covering both the ShouldBlockCorePath (core
// `xit auto`) and ShouldBlockHighRisk (VS Code bridge) gates together.
func TestFullVersionGateMatrixV2(t *testing.T) {
	cases := []struct {
		name         string
		current      string
		minCLI       string
		latestCLI    string
		wantBlocked  bool // ShouldBlockCorePath
		wantHighRisk bool // ShouldBlockHighRisk
		wantUpgrade  bool // UpgradeNeeded
	}{
		{"below minimum: blocked + bridge disabled", "0.2.48", "0.2.50", "0.2.50", true, true, true},
		{"exactly at minimum == latest: up to date, bridge enabled", "0.2.50", "0.2.50", "0.2.50", false, false, false},
		{"between minimum and latest: update available, allowed, bridge enabled", "0.2.50", "0.2.49", "0.2.51", false, false, true},
		{"ahead of latest: allowed, bridge enabled, no upgrade", "0.2.51", "0.2.50", "0.2.50", false, false, false},
		{"current == min == latest: up to date, bridge enabled", "0.2.51", "0.2.51", "0.2.51", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Server intentionally sends "required" for the non-blocked cases and
			// "blocked" doesn't even matter here except for the below-minimum
			// case — the point is the CLIENT must derive the correct effective
			// severity from current/min/latest, not merely echo the server.
			body := `{"latest_cli":"` + tc.latestCLI + `","min_cli":"` + tc.minCLI + `","severity":"required"}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			c := NewClient(t.TempDir(), tc.current)
			c.APIBase = srv.URL
			r := c.Check()

			if got := r.ShouldBlockCorePath(); got != tc.wantBlocked {
				t.Errorf("ShouldBlockCorePath() = %v, want %v", got, tc.wantBlocked)
			}
			if got := r.ShouldBlockHighRisk(); got != tc.wantHighRisk {
				t.Errorf("ShouldBlockHighRisk() = %v, want %v", got, tc.wantHighRisk)
			}
			if r.UpgradeNeeded != tc.wantUpgrade {
				t.Errorf("UpgradeNeeded = %v, want %v", r.UpgradeNeeded, tc.wantUpgrade)
			}
		})
	}
}
