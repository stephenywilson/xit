package codexhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EventInstallStatus reports the install state of one lifecycle hook.
type EventInstallStatus struct {
	Event      string
	Matcher    string
	ScriptPath string
	Installed  bool
}

type InstallResult struct {
	HooksPath        string
	Events           []EventInstallStatus
	AlreadyInstalled bool // true only if ALL four events were already installed
}

func scriptPathFor(home string, ev xitEvent) string {
	return filepath.Join(home, "hooks", ev.ScriptName)
}

func scriptBody(ev xitEvent, xitPath string) string {
	return fmt.Sprintf(`#!/bin/sh
# XiT managed Codex hook
# event: %s
# matcher: %s
exec %s codex-hook %s
`, ev.Name, ev.Matcher, xitPath, hookSubcommandFor(ev.Name))
}

func hookSubcommandFor(eventName string) string {
	switch eventName {
	case "UserPromptSubmit":
		return "user-prompt-submit"
	case "PreToolUse":
		return "pre-tool-use"
	case "PostToolUse":
		return "post-tool-use"
	case "Stop":
		return "stop"
	default:
		return strings.ToLower(eventName)
	}
}

// resolveXitPath finds an absolute path to the `xit` binary so the installed
// hook script does not depend on PATH at Codex's hook-execution time. Falls
// back to the literal "xit" if none of the common locations are found.
func resolveXitPath() string {
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), ".local", "bin", "xit"),
		"/usr/local/bin/xit",
		"/opt/homebrew/bin/xit",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "xit"
}

// Install writes the four XiT Codex lifecycle hook scripts (UserPromptSubmit,
// PreToolUse, PostToolUse, Stop) and merges them into .codex/hooks.json,
// preserving any other hooks the user has configured. Re-running Install
// upgrades a legacy PreToolUse-only install to the full lifecycle.
func Install(projectPath, home string, dryRun bool) (*InstallResult, error) {
	cfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return nil, err
	}

	events := xitEvents()
	var statuses []EventInstallStatus
	allAlready := true
	for _, ev := range events {
		installed := HasXiTHookForEvent(cfg, ev.Name, ev.Matcher)
		if !installed {
			allAlready = false
		}
		statuses = append(statuses, EventInstallStatus{
			Event:      ev.Name,
			Matcher:    ev.Matcher,
			ScriptPath: scriptPathFor(home, ev),
			Installed:  installed,
		})
	}

	if dryRun {
		return &InstallResult{
			HooksPath:        filepath.Join(projectPath, ".codex", "hooks.json"),
			Events:           statuses,
			AlreadyInstalled: allAlready,
		}, nil
	}

	xitPath := resolveXitPath()
	if err := os.MkdirAll(filepath.Join(home, "hooks"), 0755); err != nil {
		return nil, err
	}
	for i, ev := range events {
		sp := scriptPathFor(home, ev)
		if err := os.WriteFile(sp, []byte(scriptBody(ev, xitPath)), 0755); err != nil {
			return nil, err
		}
		statuses[i].Installed = true
	}

	AddXiTHook(cfg, func(ev xitEvent) string { return scriptPathFor(home, ev) })
	if err := WriteHooksConfig(projectPath, cfg); err != nil {
		return nil, err
	}

	return &InstallResult{
		HooksPath:        filepath.Join(projectPath, ".codex", "hooks.json"),
		Events:           statuses,
		AlreadyInstalled: allAlready,
	}, nil
}

// hasAnyXiTHook reports whether ANY hook command anywhere in cfg matches an
// XiT-managed script, regardless of event/matcher (matcher-agnostic — a
// partial or legacy install may use a different matcher string than the
// current default).
func hasAnyXiTHook(cfg *HooksConfig) bool {
	for _, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Type == "command" && isXiTScriptCommand(h.Command) {
					return true
				}
			}
		}
	}
	return false
}

// Uninstall removes all XiT-managed handlers from .codex/hooks.json.
func Uninstall(projectPath string) error {
	cfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return err
	}

	if !hasAnyXiTHook(cfg) {
		return fmt.Errorf("XiT Codex hook not found in %s", filepath.Join(projectPath, ".codex", "hooks.json"))
	}

	RemoveXiTHook(cfg)
	if err := WriteHooksConfig(projectPath, cfg); err != nil {
		return err
	}
	return nil
}

// codexUserConfigDir is the OS user's home directory, where Codex's own
// USER-level hook layer lives at ~/.codex/hooks.json — a real, documented
// layer distinct from the project-level <project>/.codex/hooks.json (Codex
// hooks docs: "the four most useful locations are ~/.codex/hooks.json,
// ~/.codex/config.toml, <repo>/.codex/hooks.json, <repo>/.codex/config.toml"
// and "Codex loads all matching hooks [from every layer]... higher-precedence
// config layers don't replace lower-precedence hooks" — i.e. a hook present
// in BOTH layers for the same project fires twice, concurrently).
func codexUserConfigDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// DuplicateHookCheck reports whether XiT's hook is registered in BOTH the
// project-level and the Codex user-level (~/.codex/hooks.json) layer at
// once — which per Codex's documented "loads all matching hooks, layers
// coexist" behavior would fire twice for any Bash command in this project.
type DuplicateHookCheck struct {
	ProjectLevelInstalled bool
	UserLevelInstalled    bool
	UserLevelPath         string
}

// Duplicate reports whether both layers are simultaneously active.
func (c DuplicateHookCheck) Duplicate() bool {
	return c.ProjectLevelInstalled && c.UserLevelInstalled
}

// CheckDuplicateHook inspects both the project-level and Codex user-level
// hooks.json for an XiT-managed handler. It never modifies either file.
func CheckDuplicateHook(projectPath string) (DuplicateHookCheck, error) {
	projCfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return DuplicateHookCheck{}, err
	}
	userDir := codexUserConfigDir()
	userCfg, err := ReadHooksConfig(userDir)
	if err != nil {
		return DuplicateHookCheck{}, err
	}
	return DuplicateHookCheck{
		ProjectLevelInstalled: HasXiTHook(projCfg),
		UserLevelInstalled:    HasXiTHook(userCfg),
		UserLevelPath:         filepath.Join(userDir, ".codex", "hooks.json"),
	}, nil
}

type StatusResult struct {
	HooksPath string
	Installed bool // true only if ALL four lifecycle events are installed
	Events    []EventInstallStatus
	Mode      string
	Reroute   bool
	Strict    bool
	FailOpen  bool
	HasEvents bool
}

// Status checks whether the XiT Codex hooks are installed, per event.
func Status(projectPath, home string) (*StatusResult, error) {
	cfg, err := ReadHooksConfig(projectPath)
	if err != nil {
		return nil, err
	}

	var statuses []EventInstallStatus
	allInstalled := true
	for _, ev := range xitEvents() {
		installed := HasXiTHookForEvent(cfg, ev.Name, ev.Matcher)
		if !installed {
			allInstalled = false
		}
		statuses = append(statuses, EventInstallStatus{
			Event:      ev.Name,
			Matcher:    ev.Matcher,
			ScriptPath: scriptPathFor(home, ev),
			Installed:  installed,
		})
	}

	eventsPath := filepath.Join(home, "codex-hooks", "events.jsonl")
	_, err = os.Stat(eventsPath)
	hasEvents := err == nil

	return &StatusResult{
		HooksPath: filepath.Join(projectPath, ".codex", "hooks.json"),
		Installed: allInstalled,
		Events:    statuses,
		Mode:      "observe+turn_lifecycle",
		Reroute:   true,
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
