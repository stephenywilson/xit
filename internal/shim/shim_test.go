package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/config"
)

func setTestHome(t *testing.T, dir string) {
	old := homeDirFn
	homeDirFn = func() (string, error) {
		return dir, nil
	}
	t.Cleanup(func() {
		homeDirFn = old
	})
}

func TestGenerateScript(t *testing.T) {
	script := GenerateScript("kimi", "/usr/bin/kimi", "xit")
	if !strings.HasPrefix(script, Marker) {
		t.Error("script missing XiT marker")
	}
	if !strings.Contains(script, "target: kimi") {
		t.Error("script missing target")
	}
	if !strings.Contains(script, "original: /usr/bin/kimi") {
		t.Error("script missing original path")
	}
	if !strings.Contains(script, "exec xit kimi") {
		t.Error("script missing exec command")
	}
}

func TestIsManagedShim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kimi")
	// Non-managed file
	os.WriteFile(path, []byte("#!/bin/sh\necho hello"), 0755)
	if IsManagedShim(path) {
		t.Error("non-managed file should not be a XiT shim")
	}
	// Managed file
	os.WriteFile(path, []byte(GenerateScript("kimi", "/usr/bin/kimi", "xit")), 0755)
	if !IsManagedShim(path) {
		t.Error("managed file should be a XiT shim")
	}
	// Missing file
	if IsManagedShim(filepath.Join(dir, "missing")) {
		t.Error("missing file should not be a shim")
	}
}

func TestInstallRequiresYes(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi"}
	err := Install(dir, cfg, "kimi", false, false)
	if err == nil {
		t.Fatal("install without --yes should fail")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("expected --yes error, got: %v", err)
	}
}

func TestInstallCreatesShim(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi"}

	err := Install(dir, cfg, "kimi", true, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	shimPath := filepath.Join(shimDir, "kimi")
	if _, err := os.Stat(shimPath); err != nil {
		t.Fatalf("shim not created: %v", err)
	}

	if !IsManagedShim(shimPath) {
		t.Error("created file should be a XiT shim")
	}

	// Verify config updated
	loaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	kimi := loaded.Targets["kimi"]
	if !kimi.ShimEnabled {
		t.Error("shim_enabled should be true")
	}
	if kimi.OriginalPath != "/usr/bin/kimi" {
		t.Errorf("original_path mismatch: %s", kimi.OriginalPath)
	}
	if kimi.Integration != "shim" {
		t.Errorf("integration mismatch: %s", kimi.Integration)
	}
}

func TestInstallDoesNotOverwriteNonShim(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte("#!/bin/sh\necho not xit"), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi"}

	err := Install(dir, cfg, "kimi", true, false)
	if err == nil {
		t.Fatal("install should fail when non-shim exists")
	}
	if !strings.Contains(err.Error(), "not a XiT shim") {
		t.Errorf("expected not-a-shim error, got: %v", err)
	}
}

func TestInstallUpdatesExistingShim(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte(GenerateScript("kimi", "/old/path", "xit")), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/new/path"}

	err := Install(dir, cfg, "kimi", true, false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, _ := os.ReadFile(shimPath)
	if !strings.Contains(string(data), "/new/path") {
		t.Error("shim should be updated with new path")
	}
}

func TestInstallAvoidsRecursion(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: shimPath}

	err := Install(dir, cfg, "kimi", true, false)
	if err == nil {
		t.Fatal("install should fail when path equals shim path")
	}
	if !strings.Contains(err.Error(), "--takeover") {
		t.Errorf("expected --takeover error, got: %v", err)
	}
}

func TestInstallTakeoverBacksUpOriginal(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte("#!/bin/sh\necho original kimi"), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: shimPath}

	err := Install(dir, cfg, "kimi", true, true)
	if err != nil {
		t.Fatalf("install takeover failed: %v", err)
	}

	// Shim should now be a XiT shim.
	if !IsManagedShim(shimPath) {
		t.Error("shimPath should be a XiT shim after takeover")
	}

	// Backup should exist.
	backupPath := shimPath + ".xit-original"
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup not created at %s: %v", backupPath, err)
	}
	data, _ := os.ReadFile(backupPath)
	if !strings.Contains(string(data), "original kimi") {
		t.Error("backup should contain original content")
	}

	// Config should show takeover.
	loaded, _ := config.Load(dir)
	kimi := loaded.Targets["kimi"]
	if !kimi.Takeover {
		t.Error("takeover should be true")
	}
	if kimi.BackupPath != backupPath {
		t.Errorf("backup_path mismatch: got %s", kimi.BackupPath)
	}
	if kimi.OriginalPath != backupPath {
		t.Errorf("original_path should point to backup: got %s", kimi.OriginalPath)
	}
}

func TestInstallTakeoverMultipleBackups(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte("original v1"), 0755)
	os.WriteFile(shimPath+".xit-original", []byte("old backup"), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: shimPath}

	err := Install(dir, cfg, "kimi", true, true)
	if err != nil {
		t.Fatalf("install takeover failed: %v", err)
	}

	backupPath := shimPath + ".xit-original.1"
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("numbered backup not created at %s: %v", backupPath, err)
	}
}

func TestRemoveShim(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte(GenerateScript("kimi", "/usr/bin/kimi", "xit")), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi", ShimEnabled: true, ShimPath: shimPath, Integration: "shim"}
	config.Save(dir, cfg)

	err := Remove(dir, cfg, "kimi")
	if err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	if _, err := os.Stat(shimPath); err == nil {
		t.Error("shim should be removed")
	}

	loaded, _ := config.Load(dir)
	kimi := loaded.Targets["kimi"]
	if kimi.ShimEnabled {
		t.Error("shim_enabled should be false after remove")
	}
	if kimi.Integration != "wrapper" {
		t.Errorf("integration should be wrapper, got %s", kimi.Integration)
	}
}

func TestRemoveTakeoverRestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	backupPath := shimPath + ".xit-original"
	os.WriteFile(shimPath, []byte(GenerateScript("kimi", backupPath, "xit")), 0755)
	os.WriteFile(backupPath, []byte("#!/bin/sh\necho restored original"), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: backupPath, ShimEnabled: true, ShimPath: shimPath, Integration: "shim", Takeover: true, BackupPath: backupPath}
	config.Save(dir, cfg)

	err := Remove(dir, cfg, "kimi")
	if err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	// shimPath should now be the restored original.
	if IsManagedShim(shimPath) {
		t.Error("shimPath should be restored original, not a XiT shim")
	}
	data, _ := os.ReadFile(shimPath)
	if !strings.Contains(string(data), "restored original") {
		t.Error("restored file should contain original content")
	}

	// Backup should be gone.
	if _, err := os.Stat(backupPath); err == nil {
		t.Error("backup should be removed after restore")
	}

	loaded, _ := config.Load(dir)
	kimi := loaded.Targets["kimi"]
	if kimi.Takeover {
		t.Error("takeover should be false after remove")
	}
}

func TestRemoveTakeoverMissingBackup(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte(GenerateScript("kimi", "/nonexistent", "xit")), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi", ShimEnabled: true, ShimPath: shimPath, Integration: "shim", Takeover: true, BackupPath: "/nonexistent/backup"}
	config.Save(dir, cfg)

	err := Remove(dir, cfg, "kimi")
	if err == nil {
		t.Fatal("remove should fail when backup is missing")
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Errorf("expected backup error, got: %v", err)
	}
	// Shim should still exist.
	if _, err := os.Stat(shimPath); err != nil {
		t.Error("shim should still exist when backup is missing")
	}
}

func TestRemoveDoesNotDeleteNonShim(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte("#!/bin/sh\necho not xit"), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi", ShimEnabled: true, ShimPath: shimPath}

	err := Remove(dir, cfg, "kimi")
	if err == nil {
		t.Fatal("remove should fail for non-managed file")
	}
	if !strings.Contains(err.Error(), "not a XiT managed shim") {
		t.Errorf("expected managed-shim error, got: %v", err)
	}
}

func TestStatusOutput(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{Enabled: true, Path: "/usr/bin/kimi", ShimEnabled: false}
	out := Status(dir, cfg)
	if !strings.Contains(out, "kimi:") {
		t.Error("status missing kimi")
	}
	if !strings.Contains(out, "not installed") {
		t.Error("status should show not installed")
	}
}

func TestStatusTakeover(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	shimDir := filepath.Join(dir, ".local", "bin")
	os.MkdirAll(shimDir, 0755)
	shimPath := filepath.Join(shimDir, "kimi")
	os.WriteFile(shimPath, []byte(GenerateScript("kimi", "/usr/bin/kimi.xit-original", "xit")), 0755)

	cfg := config.Default("0.2.4")
	cfg.Targets["kimi"] = config.Target{
		Enabled:      true,
		Path:         "/usr/bin/kimi",
		OriginalPath: "/usr/bin/kimi.xit-original",
		ShimPath:     shimPath,
		ShimEnabled:  true,
		Takeover:     true,
		BackupPath:   "/usr/bin/kimi.xit-original",
		Integration:  "shim",
	}
	out := Status(dir, cfg)
	if !strings.Contains(out, "takeover") {
		t.Error("status should show takeover")
	}
	if !strings.Contains(out, "installed (XiT takeover)") {
		t.Error("status should show installed (XiT takeover)")
	}
}
