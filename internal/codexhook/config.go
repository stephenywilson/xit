package codexhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// xitEvent describes one of the four lifecycle hooks XiT registers for Codex.
type xitEvent struct {
	Name       string // Codex hook event name, e.g. "PreToolUse"
	Matcher    string // "" for events with no matcher (UserPromptSubmit/Stop)
	ScriptName string // filename under <home>/hooks/
}

func xitEvents() []xitEvent {
	return []xitEvent{
		{Name: "UserPromptSubmit", Matcher: "", ScriptName: "codex-user-prompt-submit.sh"},
		{Name: "PreToolUse", Matcher: "^Bash$", ScriptName: "codex-pretooluse-bash.sh"},
		{Name: "PostToolUse", Matcher: "^Bash$", ScriptName: "codex-posttooluse-bash.sh"},
		{Name: "Stop", Matcher: "", ScriptName: "codex-stop.sh"},
	}
}

// isXiTScriptCommand reports whether a hook command string invokes one of
// XiT's managed Codex hook scripts (matched by script filename, not full path,
// so the check is robust to install location).
func isXiTScriptCommand(cmd string) bool {
	for _, ev := range xitEvents() {
		if strings.Contains(cmd, ev.ScriptName) {
			return true
		}
	}
	// Legacy marker from the pre-lifecycle (PreToolUse-only) version.
	return strings.Contains(cmd, ".xit/hooks/codex-pretooluse-bash.sh")
}

// HasXiTHookForEvent reports whether the given event (matcher must match
// exactly, "" for none) already has an XiT-managed handler registered.
func HasXiTHookForEvent(cfg *HooksConfig, eventName, matcher string) bool {
	for _, group := range cfg.Hooks[eventName] {
		if group.Matcher != matcher {
			continue
		}
		for _, h := range group.Hooks {
			if h.Type == "command" && isXiTScriptCommand(h.Command) {
				return true
			}
		}
	}
	return false
}

// HasXiTHook returns true only if ALL FOUR lifecycle events have an
// XiT-managed handler registered (full turn-lifecycle support). A partial
// install (e.g. only the legacy PreToolUse-only hook) returns false here —
// callers needing fine-grained status should use HasXiTHookForEvent per event.
func HasXiTHook(cfg *HooksConfig) bool {
	for _, ev := range xitEvents() {
		if !HasXiTHookForEvent(cfg, ev.Name, ev.Matcher) {
			return false
		}
	}
	return true
}

// RemoveXiTHookForEvent removes any XiT-managed handler from the given event,
// preserving any other (non-XiT) handlers under the same event/matcher.
func RemoveXiTHookForEvent(cfg *HooksConfig, eventName string) {
	groups, ok := cfg.Hooks[eventName]
	if !ok {
		return
	}
	var filtered []HookGroup
	for _, g := range groups {
		var cmds []HookCommand
		for _, h := range g.Hooks {
			if h.Type == "command" && isXiTScriptCommand(h.Command) {
				continue
			}
			cmds = append(cmds, h)
		}
		if len(cmds) > 0 {
			filtered = append(filtered, HookGroup{Matcher: g.Matcher, Hooks: cmds})
		}
	}
	if len(filtered) > 0 {
		cfg.Hooks[eventName] = filtered
	} else {
		delete(cfg.Hooks, eventName)
	}
}

// AddXiTHookForEvent adds the XiT handler for one event, first removing any
// existing XiT handler under that event to avoid duplicates on reinstall.
func AddXiTHookForEvent(cfg *HooksConfig, ev xitEvent, scriptPath string) {
	RemoveXiTHookForEvent(cfg, ev.Name)
	group := HookGroup{
		Matcher: ev.Matcher,
		Hooks: []HookCommand{
			{Type: "command", Command: scriptPath, Timeout: 30},
		},
	}
	cfg.Hooks[ev.Name] = append(cfg.Hooks[ev.Name], group)
}

// AddXiTHook registers all four lifecycle hooks, given a function that maps an
// event to its installed script path.
func AddXiTHook(cfg *HooksConfig, scriptPathFor func(ev xitEvent) string) {
	for _, ev := range xitEvents() {
		AddXiTHookForEvent(cfg, ev, scriptPathFor(ev))
	}
}

// RemoveXiTHook removes all four XiT-managed handlers, preserving any other
// hooks the user configured.
func RemoveXiTHook(cfg *HooksConfig) {
	for _, ev := range xitEvents() {
		RemoveXiTHookForEvent(cfg, ev.Name)
	}
}
