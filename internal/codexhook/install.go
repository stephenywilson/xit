package codexhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type InstallResult struct {
	HooksPath        string
	ScriptPath       string
	AlreadyInstalled bool
}

// Install writes the XiT Codex hook script and merges .codex/hooks.json.
func Install(projectPath, home string, dryRun bool) (*InstallResult, error) {
	scriptPath := filepath.Join(home, "hooks", "codex-pretooluse-bash.sh")

	cfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return nil, err
	}

	alreadyInstalled := HasXiTHook(cfg)

	if dryRun {
		return &InstallResult{
			HooksPath:        filepath.Join(projectPath, ".codex", "hooks.json"),
			ScriptPath:       scriptPath,
			AlreadyInstalled: alreadyInstalled,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		return nil, err
	}
	script := `#!/bin/sh
# XiT managed Codex hook
# event: PreToolUse
# matcher: Bash
exec xit codex-hook pretooluse-bash
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return nil, err
	}

	AddXiTHook(cfg, scriptPath)
	if err := WriteHooksConfig(projectPath, cfg); err != nil {
		return nil, err
	}

	return &InstallResult{
		HooksPath:        filepath.Join(projectPath, ".codex", "hooks.json"),
		ScriptPath:       scriptPath,
		AlreadyInstalled: alreadyInstalled,
	}, nil
}

// Uninstall removes the XiT handler from .codex/hooks.json.
func Uninstall(projectPath string) error {
	cfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return err
	}

	if !HasXiTHook(cfg) {
		return fmt.Errorf("XiT Codex hook not found in %s", filepath.Join(projectPath, ".codex", "hooks.json"))
	}

	RemoveXiTHook(cfg)
	if err := WriteHooksConfig(projectPath, cfg); err != nil {
		return err
	}
	return nil
}

type StatusResult struct {
	HooksPath   string
	Installed   bool
	ScriptPath  string
	Mode        string
	Reroute     bool
	Strict      bool
	FailOpen    bool
	HasEvents   bool
}

// Status checks whether the XiT Codex hook is installed.
func Status(projectPath, home string) (*StatusResult, error) {
	scriptPath := filepath.Join(home, "hooks", "codex-pretooluse-bash.sh")

	cfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return nil, err
	}

	installed := HasXiTHook(cfg)

	eventsPath := filepath.Join(home, "codex-hooks", "events.jsonl")
	_, err = os.Stat(eventsPath)
	hasEvents := err == nil

	return &StatusResult{
		HooksPath: filepath.Join(projectPath, ".codex", "hooks.json"),
		Installed: installed,
		ScriptPath: scriptPath,
		Mode:      "observe",
		Reroute:   false,
		Strict:    false,
		FailOpen:  true,
		HasEvents: hasEvents,
	}, nil
}

type StatsResult struct {
	Events      int
	Observed    int
	Passthrough int
	Errors      int
	HasEvents   bool
}

// Stats reads codex-hooks/events.jsonl and returns aggregated counts.
func Stats(home string) (*StatsResult, error) {
	result := &StatsResult{}
	path := filepath.Join(home, "codex-hooks", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result.Events++
		result.HasEvents = true
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			result.Errors++
			continue
		}
		action, _ := rec["action"].(string)
		switch action {
		case "observe", "":
			result.Observed++
		case "passthrough":
			result.Passthrough++
		case "fail_open", "error":
			result.Errors++
		default:
			result.Observed++
		}
	}
	return result, nil
}
