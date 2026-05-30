package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	c := Default("0.2.1")
	if c.Version != "0.2.1" {
		t.Errorf("version mismatch: %s", c.Version)
	}
	if c.DefaultMode != "agent" {
		t.Errorf("default_mode mismatch: %s", c.DefaultMode)
	}
	if c.Telemetry {
		t.Error("telemetry should be false by default")
	}
	if c.TokenEstimator != "bytes/4" {
		t.Errorf("token_estimator mismatch: %s", c.TokenEstimator)
	}

	for _, name := range []string{"kimi", "claude", "codex", "gemini", "cursor"} {
		target, ok := c.Targets[name]
		if !ok {
			t.Errorf("missing target: %s", name)
			continue
		}
		if target.Enabled {
			t.Errorf("target %s should be disabled by default", name)
		}
		if target.Path != "" {
			t.Errorf("target %s path should be empty by default", name)
		}
		if target.Integration != "manual" {
			t.Errorf("target %s integration should be manual by default", name)
		}
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	c := Default("0.2.1")
	c.Targets["kimi"] = Target{Enabled: true, Path: "/usr/bin/kimi", Integration: "wrapper"}

	if err := Save(dir, c); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if !Exists(dir) {
		t.Fatal("config should exist after Save")
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Version != c.Version {
		t.Errorf("loaded version mismatch: %s", loaded.Version)
	}
	if loaded.Telemetry {
		t.Error("loaded telemetry should be false")
	}

	kimi, ok := loaded.Targets["kimi"]
	if !ok {
		t.Fatal("missing kimi target")
	}
	if !kimi.Enabled {
		t.Error("kimi should be enabled")
	}
	if kimi.Path != "/usr/bin/kimi" {
		t.Errorf("kimi path mismatch: %s", kimi.Path)
	}
}

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir) {
		t.Error("config should not exist in empty dir")
	}
	_, err := Load(dir)
	if err == nil {
		t.Error("Load should fail for missing config")
	}
}

func TestConfigJSONStructure(t *testing.T) {
	c := Default("0.2.1")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if raw["telemetry"] != false {
		t.Errorf("telemetry should be false in JSON, got %v", raw["telemetry"])
	}
}

func TestFormatSummary(t *testing.T) {
	dir := t.TempDir()
	c := Default("0.2.1")
	c.Targets["kimi"] = Target{Enabled: true, Path: "/usr/bin/kimi", Integration: "wrapper"}
	c.Targets["claude"] = Target{Enabled: false, Path: "", Integration: "manual"}

	summary := c.FormatSummary(dir)
	if !contains(summary, "kimi") {
		t.Error("summary missing kimi")
	}
	if !contains(summary, "enabled") {
		t.Error("summary missing enabled status")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDetectPath(t *testing.T) {
	// go should exist in this environment
	p := DetectPath("go")
	if p == "" {
		t.Skip("go not in PATH, skipping DetectPath test")
	}
	// definitely-not-a-real-command should not exist
	p2 := DetectPath("definitely-not-a-real-command")
	if p2 != "" {
		t.Error("expected empty path for fake command")
	}
}

func TestSaveDoesNotCreateAliasOrHook(t *testing.T) {
	dir := t.TempDir()
	c := Default("0.2.1")
	if err := Save(dir, c); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Ensure no shell profile files are created
	for _, name := range []string{".zshrc", ".bashrc", ".bash_profile", ".profile"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("Save created a shell profile file: %s", name)
		}
	}

	// Ensure no hook files are created
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != DefaultFileName {
			t.Errorf("unexpected file created: %s", entry.Name())
		}
	}
}
