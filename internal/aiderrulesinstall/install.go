package aiderrulesinstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const RulesFileName = "XIT_AIDER.md"
const ConfigFileName = ".aider.conf.yml"
const ManagedBlockStart = "# XiT managed block start"
const ManagedBlockEnd = "# XiT managed block end"

const rulesContent = "# XiT / 吸T神功 rules for Aider\n\n" +
	"When running noisy terminal commands, use `xit auto`.\n\n" +
	"Use `xit auto` for:\n" +
	"- `go test -v ./...`\n" +
	"- broad `rg` / `grep`\n" +
	"- large `git diff`\n" +
	"- logs\n" +
	"- stack traces\n" +
	"- long build/test output\n" +
	"- docker logs\n" +
	"- npm test / pytest / cargo test when verbose\n\n" +
	"Do not use `xit auto` for:\n" +
	"- `git status`\n" +
	"- `pwd`\n" +
	"- small `ls`\n" +
	"- `cat` of a known short file\n" +
	"- commands where exact structured output is required\n" +
	"- install / version / json / porcelain commands\n\n" +
	"Report format after `xit auto`:\n" +
	"- command\n" +
	"- whether `xit auto` was used\n" +
	"- exit_code\n" +
	"- estimated_reduction\n" +
	"- saved_tokens\n" +
	"- raw_log path\n" +
	"- key facts\n\n" +
	"Never paste full raw output unless the user explicitly asks.\n" +
	"Raw logs stay local. Never send raw_log externally.\n" +
	"Do not modify files unless the user explicitly asks.\n"

const managedBlock = ManagedBlockStart + `
read:
  - ` + RulesFileName + `
` + ManagedBlockEnd + `
`

// Status holds the installation state for the Aider rules adapter.
type Status struct {
	Scope            string
	RulesPath        string
	RulesExists      bool
	ConfigPath       string
	ConfigExists     bool
	Installed        bool
	ReadConfigured   bool
}

// RulesPath returns the path to the rules file.
func RulesPath(root string) string {
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, RulesFileName)
}

// ConfigPath returns the path to the Aider config file.
func ConfigPath(root string) string {
	if root == "" {
		root, _ = os.Getwd()
	}
	return filepath.Join(root, ConfigFileName)
}

// Preview returns the rules content that would be installed.
func Preview() string {
	return rulesContent
}

// StatusProject checks whether the Aider rules are installed in the given root.
func StatusProject(root string) (*Status, error) {
	rulesPath := RulesPath(root)
	configPath := ConfigPath(root)

	_, rulesErr := os.Stat(rulesPath)
	_, configErr := os.Stat(configPath)

	rulesExists := rulesErr == nil
	configExists := configErr == nil

	readConfigured := false
	if configExists {
		data, err := os.ReadFile(configPath)
		if err == nil {
			readConfigured = strings.Contains(string(data), RulesFileName) &&
				strings.Contains(string(data), ManagedBlockStart)
		}
	}

	return &Status{
		Scope:          "project",
		RulesPath:      rulesPath,
		RulesExists:    rulesExists,
		ConfigPath:     configPath,
		ConfigExists:   configExists,
		Installed:      rulesExists && configExists && readConfigured,
		ReadConfigured: readConfigured,
	}, nil
}

// InstallProject installs the XiT Aider rules into the given root.
func InstallProject(root string) error {
	rulesPath := RulesPath(root)
	configPath := ConfigPath(root)

	if err := os.WriteFile(rulesPath, []byte(rulesContent), 0644); err != nil {
		return fmt.Errorf("write rules file: %w", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte(managedBlock), 0644); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read existing config: %w", err)
	}
	content := string(data)

	if strings.Contains(content, ManagedBlockStart) {
		content = replaceManagedBlock(content, managedBlock)
	} else {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + managedBlock
	}

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write updated config: %w", err)
	}
	return nil
}

// UninstallProject removes the XiT Aider rules from the given root.
func UninstallProject(root string) error {
	rulesPath := RulesPath(root)
	configPath := ConfigPath(root)

	if err := os.Remove(rulesPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove rules file: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}

	content := string(data)
	if !strings.Contains(content, ManagedBlockStart) {
		return nil
	}

	content = removeManagedBlock(content)
	content = strings.TrimSpace(content)

	if content == "" {
		if err := os.Remove(configPath); err != nil {
			return fmt.Errorf("remove config file: %w", err)
		}
		return nil
	}

	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write updated config: %w", err)
	}
	return nil
}

func replaceManagedBlock(content, newBlock string) string {
	startIdx := strings.Index(content, ManagedBlockStart)
	if startIdx == -1 {
		return content
	}
	endIdx := strings.Index(content[startIdx:], ManagedBlockEnd)
	if endIdx == -1 {
		return content
	}
	endIdx += startIdx + len(ManagedBlockEnd)
	before := content[:startIdx]
	after := content[endIdx:]
	return before + newBlock + after
}

func removeManagedBlock(content string) string {
	startIdx := strings.Index(content, ManagedBlockStart)
	if startIdx == -1 {
		return content
	}
	endIdx := strings.Index(content[startIdx:], ManagedBlockEnd)
	if endIdx == -1 {
		return content
	}
	endIdx += startIdx + len(ManagedBlockEnd)
	before := strings.TrimRight(content[:startIdx], "\n")
	after := strings.TrimLeft(content[endIdx:], "\n")
	if before == "" {
		return after
	}
	if after == "" {
		return before
	}
	return before + "\n" + after
}
