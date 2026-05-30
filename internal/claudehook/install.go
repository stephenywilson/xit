package claudehook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type InstallResult struct {
	SettingsPath     string
	BackupPath       string
	ScriptPath       string
	AlreadyInstalled bool
}

func Install(settingsPath, home string, dryRun bool) (*InstallResult, error) {
	scriptPath := filepath.Join(home, "hooks", "claude-pretooluse-bash.sh")

	settings, err := ReadSettings(settingsPath)
	if err != nil {
		return nil, err
	}

	alreadyInstalled := HasXiTHook(settings, scriptPath)

	if dryRun {
		return &InstallResult{
			SettingsPath:     settingsPath,
			ScriptPath:       scriptPath,
			AlreadyInstalled: alreadyInstalled,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		return nil, err
	}
	script := `#!/bin/sh
# XiT managed Claude Code hook
# event: PreToolUse
# matcher: Bash
exec xit claude-hook pretooluse-bash
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return nil, err
	}

	backup, err := BackupSettings(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	AddXiTHook(settings, scriptPath)

	if err := WriteSettings(settingsPath, settings); err != nil {
		return nil, err
	}

	return &InstallResult{
		SettingsPath:     settingsPath,
		BackupPath:       backup,
		ScriptPath:       scriptPath,
		AlreadyInstalled: alreadyInstalled,
	}, nil
}

func Uninstall(settingsPath, home string, dryRun bool) error {
	scriptPath := filepath.Join(home, "hooks", "claude-pretooluse-bash.sh")

	settings, err := ReadSettings(settingsPath)
	if err != nil {
		return err
	}

	if !HasXiTHook(settings, scriptPath) {
		return fmt.Errorf("XiT Claude hook not found in %s", settingsPath)
	}

	if dryRun {
		return nil
	}

	_, err = BackupSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	RemoveXiTHook(settings, scriptPath)

	if len(settings.Hooks) == 0 {
		settings.Hooks = nil
	}

	if err := WriteSettings(settingsPath, settings); err != nil {
		return err
	}

	return nil
}

type StatusResult struct {
	SettingsPath string
	Installed    bool
	ScriptPath   string
	Mode         string
	Reroute      bool
	Rewrite      bool
	FailOpen     bool
	HasEvents    bool
}

func Status(settingsPath, home string) (*StatusResult, error) {
	scriptPath := filepath.Join(home, "hooks", "claude-pretooluse-bash.sh")

	settings, err := ReadSettings(settingsPath)
	if err != nil {
		return nil, err
	}

	installed := HasXiTHook(settings, scriptPath)

	cfg, _ := ReadHookConfig(home)
	if cfg == nil {
		cfg = DefaultHookConfig()
	}

	eventsPath := filepath.Join(home, "claude-hooks", "events.jsonl")
	_, err = os.Stat(eventsPath)
	hasEvents := err == nil

	return &StatusResult{
		SettingsPath: settingsPath,
		Installed:    installed,
		ScriptPath:   scriptPath,
		Mode:         cfg.Mode,
		Reroute:      cfg.Mode == "reroute",
		Rewrite:      false,
		FailOpen:     cfg.FailOpen,
		HasEvents:    hasEvents,
	}, nil
}

func EnableReroute(home string) error {
	cfg, err := ReadHookConfig(home)
	if err != nil {
		return err
	}
	cfg.Mode = "reroute"
	return WriteHookConfig(home, cfg)
}

func DisableReroute(home string) error {
	cfg, err := ReadHookConfig(home)
	if err != nil {
		return err
	}
	cfg.Mode = "observe"
	return WriteHookConfig(home, cfg)
}

type StatsResult struct {
	Events       int
	Observed     int
	Rerouted     int
	Passthrough  int
	Errors       int
	TopCommands  []CommandCount
	HasEvents    bool
}

type CommandCount struct {
	Command string
	Count   int
}

func Stats(home string) (*StatsResult, error) {
	result := &StatsResult{}
	path := filepath.Join(home, "claude-hooks", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	counts := make(map[string]int)
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
		case "reroute":
			result.Rerouted++
			cmd, _ := rec["original_command"].(string)
			if cmd != "" {
				counts[cmd]++
			}
		case "passthrough":
			result.Passthrough++
		case "error_fail_open":
			result.Errors++
		default:
			result.Observed++
		}
	}
	for cmd, c := range counts {
		result.TopCommands = append(result.TopCommands, CommandCount{Command: cmd, Count: c})
	}
	return result, nil
}
