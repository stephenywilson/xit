package aiderrulesinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewIncludesXitAuto(t *testing.T) {
	p := Preview()
	if !strings.Contains(p, "xit auto") {
		t.Error("preview missing xit auto")
	}
}

func TestPreviewIncludesSavedTokens(t *testing.T) {
	p := Preview()
	if !strings.Contains(p, "saved_tokens") {
		t.Error("preview missing saved_tokens")
	}
}

func TestInstallCreatesRulesFile(t *testing.T) {
	tmp := t.TempDir()
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, RulesFileName)
	if _, err := os.Stat(path); err != nil {
		t.Error("rules file not created")
	}
}

func TestInstallCreatesConfigFile(t *testing.T) {
	tmp := t.TempDir()
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, ConfigFileName)
	if _, err := os.Stat(path); err != nil {
		t.Error("config file not created")
	}
}

func TestConfigReferencesRulesFile(t *testing.T) {
	tmp := t.TempDir()
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), RulesFileName) {
		t.Error("config does not reference rules file")
	}
}

func TestInstallIdempotent(t *testing.T) {
	tmp := t.TempDir()
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	data1, _ := os.ReadFile(filepath.Join(tmp, ConfigFileName))
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(filepath.Join(tmp, ConfigFileName))
	c1 := strings.Count(string(data1), RulesFileName)
	c2 := strings.Count(string(data2), RulesFileName)
	if c2 != c1 {
		t.Errorf("idempotency failed: %d refs before, %d after", c1, c2)
	}
}

func TestInstallPreservesExistingConfig(t *testing.T) {
	tmp := t.TempDir()
	existing := "model: gpt-4o\n"
	configPath := filepath.Join(tmp, ConfigFileName)
	os.WriteFile(configPath, []byte(existing), 0644)

	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "model: gpt-4o") {
		t.Error("existing config content lost")
	}
	if !strings.Contains(string(data), RulesFileName) {
		t.Error("rules file reference missing")
	}
}

func TestUninstallRemovesReadEntry(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, ConfigFileName)
	os.WriteFile(configPath, []byte("model: gpt-4o\n"), 0644)
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	if err := UninstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), RulesFileName) {
		t.Error("read entry not removed")
	}
	if !strings.Contains(string(data), "model: gpt-4o") {
		t.Error("existing content lost")
	}
}

func TestUninstallRemovesRulesFile(t *testing.T) {
	tmp := t.TempDir()
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	if err := UninstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, RulesFileName)); !os.IsNotExist(err) {
		t.Error("rules file not removed")
	}
}

func TestUninstallDoesNotDeleteUserConfig(t *testing.T) {
	tmp := t.TempDir()
	existing := "model: gpt-4o\n"
	configPath := filepath.Join(tmp, ConfigFileName)
	os.WriteFile(configPath, []byte(existing), 0644)

	if err := UninstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Error("user config deleted unexpectedly")
	}
}

func TestUninstallRemovesXiTCreatedConfig(t *testing.T) {
	tmp := t.TempDir()
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	if err := UninstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ConfigFileName)); !os.IsNotExist(err) {
		t.Error("XiT-created config not removed")
	}
}

func TestStatusDetectsInstalled(t *testing.T) {
	tmp := t.TempDir()
	st, _ := StatusProject(tmp)
	if st.Installed {
		t.Error("status should not report installed before install")
	}
	if err := InstallProject(tmp); err != nil {
		t.Fatal(err)
	}
	st, _ = StatusProject(tmp)
	if !st.Installed {
		t.Error("status should report installed after install")
	}
}
