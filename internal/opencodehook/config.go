package opencodehook

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// HookConfig controls XiT OpenCode hook behavior.
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

func configPath(home string) string {
	return filepath.Join(home, "opencode-hooks", "config.json")
}

func ReadHookConfig(home string) (*HookConfig, error) {
	p := configPath(home)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultHookConfig(), nil
		}
		return nil, err
	}
	var cfg HookConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func WriteHookConfig(home string, cfg *HookConfig) error {
	p := configPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0644)
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
