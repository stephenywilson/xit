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
	Handlers []HookHandler `json:"handlers"`
}

// HookHandler defines a single hook handler entry.
type HookHandler struct {
	Event   string      `json:"event"`
	Matcher HookMatcher `json:"matcher,omitempty"`
	Command string      `json:"command"`
}

// HookMatcher filters which events trigger the handler.
type HookMatcher struct {
	Tool string `json:"tool,omitempty"`
}

// ReadHooksConfig reads .codex/hooks.json from the given project path.
func ReadHooksConfig(projectPath string) (*HooksConfig, error) {
	path := filepath.Join(projectPath, ".codex", "hooks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HooksConfig{Handlers: []HookHandler{}}, nil
		}
		return nil, err
	}
	var cfg HooksConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid hooks.json: %w", err)
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
	for _, h := range cfg.Handlers {
		if h.Event == "pre_tool_use" && strings.Contains(h.Command, xitHookMarker) {
			return true
		}
	}
	return false
}

// AddXiTHook adds the XiT PreToolUse Bash handler, preserving existing handlers.
func AddXiTHook(cfg *HooksConfig, scriptPath string) {
	// Remove any existing XiT handler to avoid duplicates.
	RemoveXiTHook(cfg)
	cfg.Handlers = append(cfg.Handlers, HookHandler{
		Event: "pre_tool_use",
		Matcher: HookMatcher{
			Tool: "Bash",
		},
		Command: scriptPath,
	})
}

// RemoveXiTHook removes the XiT PreToolUse Bash handler, preserving others.
func RemoveXiTHook(cfg *HooksConfig) {
	filtered := make([]HookHandler, 0, len(cfg.Handlers))
	for _, h := range cfg.Handlers {
		if h.Event == "pre_tool_use" && strings.Contains(h.Command, xitHookMarker) {
			continue
		}
		filtered = append(filtered, h)
	}
	cfg.Handlers = filtered
}
