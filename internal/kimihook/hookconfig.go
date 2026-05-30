package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type HookConfig struct {
	Mode          string `json:"mode"`
	RerouteEnabled bool   `json:"reroute_enabled"`
	FailOpen      bool   `json:"fail_open"`
	InlineStatus  bool   `json:"inline_status"`
	StatusStyle   string `json:"status_style"`
}

func DefaultHookConfig() *HookConfig {
	return &HookConfig{
		Mode:           "observe",
		RerouteEnabled: false,
		FailOpen:       true,
		InlineStatus:   true,
		StatusStyle:    "compact",
	}
}

func HookConfigPath(home string) string {
	return filepath.Join(home, "kimi-hooks", "config.json")
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
	if cfg.StatusStyle == "" {
		cfg.StatusStyle = "compact"
	}
	return &cfg, nil
}

func SetStatusStyle(home string, style string) error {
	cfg, err := ReadHookConfig(home)
	if err != nil {
		return err
	}
	cfg.StatusStyle = style
	return WriteHookConfig(home, cfg)
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

func EnableReroute(home string) error {
	cfg, err := ReadHookConfig(home)
	if err != nil {
		return err
	}
	cfg.Mode = "reroute"
	cfg.RerouteEnabled = true
	return WriteHookConfig(home, cfg)
}

func DisableReroute(home string) error {
	cfg, err := ReadHookConfig(home)
	if err != nil {
		return err
	}
	cfg.Mode = "observe"
	cfg.RerouteEnabled = false
	return WriteHookConfig(home, cfg)
}
