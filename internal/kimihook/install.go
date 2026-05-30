package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type InstallResult struct {
	ConfigPath       string
	BackupPath       string
	ScriptPath       string
	AlreadyInstalled bool
	Format           ConfigFormat
}

func turnLifecycleScriptName(event string) string {
	return fmt.Sprintf("kimi-turn-%s.sh", strings.ToLower(event))
}

func Install(configPath, home string, dryRun bool) (*InstallResult, error) {
	scriptPath := filepath.Join(home, "hooks", "kimi-pretooluse-shell.sh")
	format := DetectConfigFormat(configPath)
	if format == FormatNone {
		// Default to TOML for new files.
		format = FormatTOML
	}

	alreadyInstalled := false

	// Prepare 4 explicit turn lifecycle scripts.
	turnScripts := map[string]string{}
	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionStart", "SessionEnd"} {
		name := turnLifecycleScriptName(event)
		turnScripts[event] = filepath.Join(home, "hooks", name)
	}

	switch format {
	case FormatTOML:
		content, err := ReadToml(configPath)
		if err != nil {
			return nil, err
		}
		alreadyInstalled = HasXiTHookToml(content)
		if dryRun {
			return &InstallResult{
				ConfigPath:       configPath,
				ScriptPath:       scriptPath,
				AlreadyInstalled: alreadyInstalled,
				Format:           format,
			}, nil
		}
		if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
			return nil, err
		}
		script := `#!/bin/sh
# XiT managed Kimi Code hook
# event: PreToolUse
# matcher: Shell/Bash
# mode: observe
exec xit kimi-hook observe
`
		if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
			return nil, err
		}
		for event, path := range turnScripts {
			turnScript := fmt.Sprintf(`#!/bin/sh
# XiT managed Kimi Code turn lifecycle hook
# event: %s
exec xit kimi-hook turn %s
`, event, event)
			if err := os.WriteFile(path, []byte(turnScript), 0755); err != nil {
				return nil, err
			}
		}
		backup, err := BackupSettings(configPath)
		if err != nil {
			return nil, fmt.Errorf("backup failed: %w", err)
		}
		newContent, err := AddXiTHookToml(content, scriptPath, turnScripts)
		if err != nil {
			return nil, err
		}
		if err := WriteToml(configPath, newContent); err != nil {
			return nil, err
		}
		return &InstallResult{
			ConfigPath:       configPath,
			BackupPath:       backup,
			ScriptPath:       scriptPath,
			AlreadyInstalled: alreadyInstalled,
			Format:           format,
		}, nil

	case FormatJSON:
		settings, err := ReadSettings(configPath)
		if err != nil {
			return nil, err
		}
		alreadyInstalled = HasXiTHook(settings, scriptPath)
		if dryRun {
			return &InstallResult{
				ConfigPath:       configPath,
				ScriptPath:       scriptPath,
				AlreadyInstalled: alreadyInstalled,
				Format:           format,
			}, nil
		}
		if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
			return nil, err
		}
		script := `#!/bin/sh
# XiT managed Kimi Code hook
# event: PreToolUse (beta schema)
# matcher: Bash
# mode: observe
exec xit kimi-hook observe
`
		if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
			return nil, err
		}
		for event, path := range turnScripts {
			turnScript := fmt.Sprintf(`#!/bin/sh
# XiT managed Kimi Code turn lifecycle hook
# event: %s
exec xit kimi-hook turn %s
`, event, event)
			if err := os.WriteFile(path, []byte(turnScript), 0755); err != nil {
				return nil, err
			}
		}
		backup, err := BackupSettings(configPath)
		if err != nil {
			return nil, fmt.Errorf("backup failed: %w", err)
		}
		AddXiTHook(settings, scriptPath, turnScripts)
		if err := WriteSettings(configPath, settings); err != nil {
			return nil, err
		}
		return &InstallResult{
			ConfigPath:       configPath,
			BackupPath:       backup,
			ScriptPath:       scriptPath,
			AlreadyInstalled: alreadyInstalled,
			Format:           format,
		}, nil
	}

	return nil, fmt.Errorf("unsupported config format: %s", format)
}

func Uninstall(configPath, home string, dryRun bool) error {
	scriptPath := filepath.Join(home, "hooks", "kimi-pretooluse-shell.sh")
	format := DetectConfigFormat(configPath)

	// Clean up turn lifecycle scripts.
	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionStart", "SessionEnd"} {
		path := filepath.Join(home, "hooks", turnLifecycleScriptName(event))
		_ = os.Remove(path)
	}

	switch format {
	case FormatTOML:
		content, err := ReadToml(configPath)
		if err != nil {
			return err
		}
		if !HasXiTHookToml(content) {
			return fmt.Errorf("XiT Kimi hook not found in %s", configPath)
		}
		if dryRun {
			return nil
		}
		_, err = BackupSettings(configPath)
		if err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}
		newContent := RemoveXiTHookToml(content)
		// Clean up trailing blank lines.
		newContent = strings.TrimRight(newContent, "\n") + "\n"
		if err := WriteToml(configPath, newContent); err != nil {
			return err
		}
		return nil

	case FormatJSON:
		settings, err := ReadSettings(configPath)
		if err != nil {
			return err
		}
		if !HasXiTHook(settings, scriptPath) {
			return fmt.Errorf("XiT Kimi hook not found in %s", configPath)
		}
		if dryRun {
			return nil
		}
		_, err = BackupSettings(configPath)
		if err != nil {
			return fmt.Errorf("backup failed: %w", err)
		}
		RemoveXiTHook(settings, scriptPath)
		if len(settings.Hooks) == 0 {
			settings.Hooks = nil
		}
		if err := WriteSettings(configPath, settings); err != nil {
			return err
		}
		return nil

	case FormatNone:
		return fmt.Errorf("XiT Kimi hook not found in %s", configPath)
	}

	return fmt.Errorf("unsupported config format: %s", format)
}

type StatusResult struct {
	ConfigPath       string
	Installed        bool
	ScriptPath       string
	HasEvents        bool
	Format           ConfigFormat
	HasConflict      bool
	Mode             string
	Reroute          bool
	InlineStatus     bool
	StatusStyle      string
	TurnLifecycle    bool
	TurnEvents       map[string]bool
	TurnScripts      map[string]bool
}

func Status(configPath, home string) (*StatusResult, error) {
	scriptPath := filepath.Join(home, "hooks", "kimi-pretooluse-shell.sh")
	format := DetectConfigFormat(configPath)
	installed := false
	hasConflict := false
	turnLifecycle := false
	turnEvents := map[string]bool{
		"UserPromptSubmit": false,
		"Stop":             false,
		"SessionStart":     false,
		"SessionEnd":       false,
	}
	turnScripts := map[string]bool{
		"UserPromptSubmit": false,
		"Stop":             false,
		"SessionStart":     false,
		"SessionEnd":       false,
	}

	switch format {
	case FormatTOML:
		content, err := ReadToml(configPath)
		if err != nil {
			return nil, err
		}
		installed = HasXiTHookToml(content)
		hasConflict = HasHooksConflictToml(content)
		turnLifecycle = HasXiTTurnHookToml(content)
		for event := range turnEvents {
			turnEvents[event] = HasXiTEventToml(content, event)
		}
	case FormatJSON:
		settings, err := ReadSettings(configPath)
		if err != nil {
			return nil, err
		}
		installed = HasXiTHook(settings, scriptPath)
		turnLifecycle = HasXiTTurnHook(settings, scriptPath)
		for event := range turnEvents {
			turnEvents[event] = HasXiTEvent(settings, event, scriptPath)
		}
	}

	// Check explicit scripts exist/executable.
	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionStart", "SessionEnd"} {
		path := filepath.Join(home, "hooks", turnLifecycleScriptName(event))
		if info, err := os.Stat(path); err == nil && info.Mode()&0111 != 0 {
			turnScripts[event] = true
		}
	}

	eventsPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	_, err := os.Stat(eventsPath)
	hasEvents := err == nil

	cfg, _ := ReadHookConfig(home)
	mode := "observe"
	reroute := false
	inlineStatus := true
	statusStyle := "compact"
	if cfg != nil {
		mode = cfg.Mode
		reroute = cfg.RerouteEnabled
		inlineStatus = cfg.InlineStatus
		statusStyle = cfg.StatusStyle
	}

	return &StatusResult{
		ConfigPath:    configPath,
		Installed:     installed,
		ScriptPath:    scriptPath,
		HasEvents:     hasEvents,
		Format:        format,
		HasConflict:   hasConflict,
		Mode:          mode,
		Reroute:       reroute,
		InlineStatus:  inlineStatus,
		StatusStyle:   statusStyle,
		TurnLifecycle: turnLifecycle,
		TurnEvents:    turnEvents,
		TurnScripts:   turnScripts,
	}, nil
}

type StatsResult struct {
	Events      int
	Observed    int
	Rerouted    int
	Passthrough int
	Errors      int
	TopCommands []CommandCount
	HasEvents   bool
}

type CommandCount struct {
	Command string
	Count   int
}

func Stats(home string) (*StatsResult, error) {
	result := &StatsResult{}
	path := filepath.Join(home, "kimi-hooks", "events.jsonl")
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
