package cursorhook

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

// Install writes the XiT Cursor hook script and merges ~/.cursor/hooks.json.
func Install(hooksPath, home string, dryRun bool) (*InstallResult, error) {
	scriptPath := filepath.Join(home, "hooks", "cursor-before-shell-exec.sh")

	cfg, err := ReadHooksConfig(hooksPath)
	if err != nil {
		return nil, err
	}

	alreadyInstalled := HasXiTHook(cfg)

	if dryRun {
		return &InstallResult{
			HooksPath:        hooksPath,
			ScriptPath:       scriptPath,
			AlreadyInstalled: alreadyInstalled,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		return nil, err
	}

	// Resolve absolute path to xit for reliable execution inside Cursor.
	xitPath, err := os.Executable()
	if err != nil {
		xitPath = "xit"
	}

	script := fmt.Sprintf(`#!/bin/sh
# XiT managed Cursor hook
# event: beforeShellExecution
exec %s cursor-hook before-shell-exec
`, xitPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return nil, err
	}

	AddXiTHook(cfg, scriptPath)
	if err := WriteHooksConfig(hooksPath, cfg); err != nil {
		return nil, err
	}

	return &InstallResult{
		HooksPath:        hooksPath,
		ScriptPath:       scriptPath,
		AlreadyInstalled: alreadyInstalled,
	}, nil
}

// Uninstall removes the XiT handler from ~/.cursor/hooks.json.
func Uninstall(hooksPath, home string) error {
	scriptPath := filepath.Join(home, "hooks", "cursor-before-shell-exec.sh")

	cfg, err := ReadHooksConfig(hooksPath)
	if err != nil {
		return err
	}

	if !HasXiTHook(cfg) {
		return fmt.Errorf("XiT Cursor hook not found in %s", hooksPath)
	}

	RemoveXiTHook(cfg)
	if err := WriteHooksConfig(hooksPath, cfg); err != nil {
		return err
	}

	_ = os.Remove(scriptPath)
	return nil
}

type StatusResult struct {
	HooksPath   string
	Installed   bool
	ScriptPath  string
	Mode        string
	FailOpen    bool
	HasEvents   bool
}

// Status checks whether the XiT Cursor hook is installed.
func Status(hooksPath, home string) (*StatusResult, error) {
	scriptPath := filepath.Join(home, "hooks", "cursor-before-shell-exec.sh")

	cfg, err := ReadHooksConfig(hooksPath)
	if err != nil {
		return nil, err
	}

	installed := HasXiTHook(cfg)

	eventsPath := filepath.Join(home, "cursor-hooks", "events.jsonl")
	_, err = os.Stat(eventsPath)
	hasEvents := err == nil

	return &StatusResult{
		HooksPath:  hooksPath,
		Installed:  installed,
		ScriptPath: scriptPath,
		Mode:       "observe",
		FailOpen:   true,
		HasEvents:  hasEvents,
	}, nil
}

type StatsResult struct {
	Events      int
	Observed    int
	Passthrough int
	Errors      int
	HasEvents   bool
}

// Stats reads cursor-hooks/events.jsonl and returns aggregated counts.
func Stats(home string) (*StatsResult, error) {
	result := &StatsResult{}
	path := filepath.Join(home, "cursor-hooks", "events.jsonl")
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
