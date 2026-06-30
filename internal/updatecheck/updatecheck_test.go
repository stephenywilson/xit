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
	if !r.ShouldBlockHighRisk() {
		t.Fatal("required severity should block high-risk paths")
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
// policy (spec §六): ONLY severity=blocked refuses the core `xit auto` path.
// info/recommended/required must never block the command itself.
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
		{"blocked", `{"latest_cli":"0.2.49","min_cli":"0.2.40","severity":"blocked"}`, true},
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
