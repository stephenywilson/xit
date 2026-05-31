package codexhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const xitHookMarker = ".xit/hooks/codex-pretooluse-bash.sh"

// HooksConfig is the project-level .codex/hooks.json format.
type HooksConfig struct {
	Hooks map[string][]HookGroup `json:"hooks"`
}

// HookGroup groups hooks by matcher under an event.
type HookGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []HookCommand `json:"hooks"`
}

// HookCommand is a single command handler inside a group.
type HookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// ReadHooksConfig reads .codex/hooks.json from the given project path.
func ReadHooksConfig(projectPath string) (*HooksConfig, error) {
	path := filepath.Join(projectPath, ".codex", "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HooksConfig{Hooks: map[string][]HookGroup{}}, nil
		}
		return nil, err
	}
	var cfg HooksConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid hooks.json: %w", err)
	}
	if cfg.Hooks == nil {
		cfg.Hooks = map[string][]HookGroup{}
	}
	return &cfg, nil
}

// WriteHooksConfig writes .codex/hooks.json.
func WriteHooksConfig(projectPath string, cfg *HooksConfig) error {
	dir := filepath.Join(projectPath, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "hooks.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// HasXiTHook returns true if the XiT PreToolUse Bash hook is installed.
func HasXiTHook(cfg *HooksConfig) bool {
	for _, group := range cfg.Hooks["PreToolUse"] {
		if group.Matcher != "Bash" {
			continue
		}
		for _, h := range group.Hooks {
			if h.Type == "command" && strings.Contains(h.Command, xitHookMarker) {
				return true
			}
		}
	}
	return false
}

// AddXiTHook adds the XiT PreToolUse Bash handler, preserving existing hooks.
func AddXiTHook(cfg *HooksConfig, scriptPath string) {
	// Remove any existing XiT handler to avoid duplicates.
	RemoveXiTHook(cfg)
	group := HookGroup{
		Matcher: "Bash",
		Hooks: []HookCommand{
			{Type: "command", Command: scriptPath, Timeout: 30},
		},
	}
	cfg.Hooks["PreToolUse"] = append(cfg.Hooks["PreToolUse"], group)
}

// RemoveXiTHook removes the XiT PreToolUse Bash handler, preserving others.
func RemoveXiTHook(cfg *HooksConfig) {
	groups, ok := cfg.Hooks["PreToolUse"]
	if !ok {
		return
	}
	var filtered []HookGroup
	for _, g := range groups {
		if g.Matcher != "Bash" {
			filtered = append(filtered, g)
			continue
		}
		var cmds []HookCommand
		for _, h := range g.Hooks {
			if h.Type == "command" && strings.Contains(h.Command, xitHookMarker) {
				continue
			}
			cmds = append(cmds, h)
		}
		if len(cmds) > 0 {
			filtered = append(filtered, HookGroup{Matcher: g.Matcher, Hooks: cmds})
		}
	}
	if len(filtered) > 0 {
		cfg.Hooks["PreToolUse"] = filtered
	} else {
		delete(cfg.Hooks, "PreToolUse")
	}
}
