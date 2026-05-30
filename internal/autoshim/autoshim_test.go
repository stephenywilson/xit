package autoshim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateShims(t *testing.T) {
	sessionDir := t.TempDir()
	shimDir, envVars, err := CreateShims(sessionDir, "/usr/local/bin/xit", []string{"git", "go"})
	if err != nil {
		t.Fatalf("CreateShims failed: %v", err)
	}
	if shimDir == "" {
		t.Fatal("shimDir should not be empty")
	}

	// Check shim files exist
	for _, tool := range []string{"git", "go"} {
		p := filepath.Join(shimDir, tool)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("shim %s not created: %v", tool, err)
		}
		data, _ := os.ReadFile(p)
		if !strings.HasPrefix(string(data), ShimMarker) {
			t.Errorf("shim %s missing marker", tool)
		}
		if !strings.Contains(string(data), "auto "+tool) {
			t.Errorf("shim %s missing auto command", tool)
		}
	}

	// Check env vars contain XIT_BIN
	foundBin := false
	for _, e := range envVars {
		if strings.HasPrefix(e, "XIT_BIN=/usr/local/bin/xit") {
			foundBin = true
		}
	}
	if !foundBin {
		t.Error("missing XIT_BIN env var")
	}
}

func TestIsManagedShim(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "git")
	os.WriteFile(p, []byte(ShimMarker+"\nexec xit auto git"), 0644)
	if !IsManagedShim(p) {
		t.Error("should recognize XiT shim")
	}
	os.WriteFile(p, []byte("#!/bin/sh\necho hello"), 0644)
	if IsManagedShim(p) {
		t.Error("should not recognize non-shim")
	}
}

func TestStripShimDirsFromPath(t *testing.T) {
	input := "/usr/bin:/Users/x/.xit/sessions/abc/shims:/usr/local/bin"
	out := stripShimDirsFromPath(input)
	if strings.Contains(out, "shims") {
		t.Error("shim dir should be stripped")
	}
	if !strings.Contains(out, "/usr/bin") {
		t.Error("should preserve non-shim dirs")
	}
}
