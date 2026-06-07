package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const DefaultFileName = "config.json"

type Config struct {
	Version        string            `json:"version"`
	DefaultMode    string            `json:"default_mode"`
	TokenEstimator string            `json:"token_estimator"`
	Telemetry      bool              `json:"telemetry"`
	Targets        map[string]Target `json:"targets"`
}

type Target struct {
	Enabled      bool   `json:"enabled"`
	Path         string `json:"path"`
	OriginalPath string `json:"original_path"`
	ShimPath     string `json:"shim_path"`
	ShimEnabled  bool   `json:"shim_enabled"`
	Takeover     bool   `json:"takeover"`
	BackupPath   string `json:"backup_path"`
	Integration  string `json:"integration"`
	Wrapper      bool   `json:"wrapper"`
}

func defaultTargets() map[string]Target {
	names := []string{"kimi", "claude", "codex", "gemini", "cursor", "opencode"}
	t := make(map[string]Target)
	for _, n := range names {
		wrapper := n != "cursor"
		t[n] = Target{
			Enabled:      false,
			Path:         "",
			OriginalPath: "",
			ShimPath:     "",
			ShimEnabled:  false,
			Takeover:     false,
			BackupPath:   "",
			Integration:  "manual",
			Wrapper:      wrapper,
		}
	}
	return t
}

func Default(version string) *Config {
	return &Config{
		Version:        version,
		DefaultMode:    "agent",
		TokenEstimator: "bytes/4",
		Telemetry:      false,
		Targets:        defaultTargets(),
	}
}

func Path(home string) string {
	return filepath.Join(home, DefaultFileName)
}

func Load(home string) (*Config, error) {
	p := Path(home)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func Save(home string, c *Config) error {
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	p := Path(home)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0644)
}

func Exists(home string) bool {
	_, err := os.Stat(Path(home))
	return err == nil
}

func DetectPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func (c *Config) FormatSummary(home string) string {
	var b strings.Builder
	b.WriteString("XiT Config\n\n")
	b.WriteString(fmt.Sprintf("path:         %s\n", Path(home)))
	b.WriteString(fmt.Sprintf("version:      %s\n", c.Version))
	b.WriteString(fmt.Sprintf("default_mode: %s\n", c.DefaultMode))
	b.WriteString(fmt.Sprintf("telemetry:    %v\n", c.Telemetry))
	b.WriteString("\ntargets:\n\n")
	for _, name := range []string{"kimi", "claude", "codex", "gemini", "cursor", "opencode"} {
		t, ok := c.Targets[name]
		if !ok {
			continue
		}
		status := "disabled"
		if t.Enabled {
			status = "enabled"
		}
		found := t.Path
		if found == "" {
			found = "not found"
		}
		b.WriteString(fmt.Sprintf("* %s: %s, %s, %s", name, status, t.Integration, found))
		if t.ShimEnabled {
			b.WriteString(fmt.Sprintf(", shim=%s", t.ShimPath))
		}
		if t.Takeover {
			b.WriteString(", takeover")
		}
		b.WriteString("\n")
	}
	return b.String()
}
