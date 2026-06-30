package apibase

import "testing"

func TestResolveEnvOverridesDefault(t *testing.T) {
	old := Default
	defer func() { Default = old }()

	Default = "https://xit-api.example.com"
	t.Setenv("XIT_API_BASE", "http://127.0.0.1:8787/")
	if got := Resolve(); got != "http://127.0.0.1:8787" {
		t.Fatalf("env must override default and strip trailing slash, got %q", got)
	}
	if Source() != "XIT_API_BASE env" {
		t.Fatalf("source should be env, got %q", Source())
	}
}

func TestResolveFallsBackToDefault(t *testing.T) {
	old := Default
	defer func() { Default = old }()

	Default = "https://xit-api.example.com/"
	t.Setenv("XIT_API_BASE", "")
	if got := Resolve(); got != "https://xit-api.example.com" {
		t.Fatalf("should fall back to default (trimmed), got %q", got)
	}
	if Source() != "built-in default" {
		t.Fatalf("source should be built-in default, got %q", Source())
	}
}

// TestResolveEmptyWhenUnprovisioned: dev/test builds may still set an empty
// Default. With no env, Resolve() is empty so both clients no-op (fail-open).
func TestResolveEmptyWhenUnprovisioned(t *testing.T) {
	old := Default
	defer func() { Default = old }()

	Default = ""
	t.Setenv("XIT_API_BASE", "")
	if got := Resolve(); got != "" {
		t.Fatalf("unprovisioned build must resolve to empty, got %q", got)
	}
	if Source() != "unset (no endpoint configured)" {
		t.Fatalf("source should report unset, got %q", Source())
	}
}

func TestSourceConstantIsProductionAPI(t *testing.T) {
	if Default != "https://xit-api.stephenwilson.dev" {
		t.Fatalf("apibase.Default = %q, want production API base", Default)
	}
}
