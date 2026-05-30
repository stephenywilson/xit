package claudehook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type HookConfig struct {
	Mode     string `json:"mode"`
	FailOpen bool   `json:"fail_open"`
}

func DefaultHookConfig() *HookConfig {
	return &HookConfig{
		Mode:     "observe",
		FailOpen: true,
	}
}

func HookConfigPath(home string) string {
	return filepath.Join(home, "claude-hooks", "config.json")
}

func ReadHookConfig(home string) (*HookConfig, error) {
	p := HookConfigPath(home)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultHookConfig(), nil
		}
		return nil, err
	}
	var cfg HookConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid hook config JSON: %w", err)
	}
	if cfg.Mode == "" {
		cfg.Mode = "observe"
	}
	return &cfg, nil
}

func WriteHookConfig(home string, cfg *HookConfig) error {
	p := HookConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0644)
}
