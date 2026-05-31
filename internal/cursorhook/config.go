package cursorhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HooksConfig is the top-level Cursor hooks.json schema.
type HooksConfig struct {
	Hooks   map[string][]HookEntry `json:"hooks"`
	Version int                      `json:"version,omitempty"`
}

// HookEntry is a single hook command entry.
type HookEntry struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// UserHooksPath returns the user-level Cursor hooks.json path.
func UserHooksPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cursor", "hooks.json")
}

// ReadHooksConfig reads and parses the Cursor hooks.json.
func ReadHooksConfig(path string) (*HooksConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &HooksConfig{Hooks: make(map[string][]HookEntry), Version: 1}, nil
		}
		return nil, err
	}
	var cfg HooksConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse hooks.json: %w", err)
	}
	if cfg.Hooks == nil {
		cfg.Hooks = make(map[string][]HookEntry)
	}
	return &cfg, nil
}

// WriteHooksConfig writes the Cursor hooks.json atomically.
func WriteHooksConfig(path string, cfg *HooksConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// XiTHookMarker is the substring we look for to identify XiT-managed hook entries.
const XiTHookMarker = "cursor-before-shell-exec"

// HasXiTHook checks whether the XiT hook is already installed in the config.
func HasXiTHook(cfg *HooksConfig) bool {
	for _, entries := range cfg.Hooks {
		for _, e := range entries {
			if strings.Contains(e.Command, XiTHookMarker) {
				return true
			}
		}
	}
	return false
}

// AddXiTHook adds the XiT beforeShellExecution hook entry.
func AddXiTHook(cfg *HooksConfig, scriptPath string) {
	key := "beforeShellExecution"
	cleaned := make([]HookEntry, 0, len(cfg.Hooks[key]))
	for _, e := range cfg.Hooks[key] {
		if !strings.Contains(e.Command, XiTHookMarker) {
			cleaned = append(cleaned, e)
		}
	}
	cleaned = append(cleaned, HookEntry{Command: scriptPath, Timeout: 30})
	cfg.Hooks[key] = cleaned
}

// RemoveXiTHook removes the XiT hook entry from beforeShellExecution.
func RemoveXiTHook(cfg *HooksConfig) {
	key := "beforeShellExecution"
	cleaned := make([]HookEntry, 0, len(cfg.Hooks[key]))
	for _, e := range cfg.Hooks[key] {
		if !strings.Contains(e.Command, XiTHookMarker) {
			cleaned = append(cleaned, e)
		}
	}
	cfg.Hooks[key] = cleaned
}
