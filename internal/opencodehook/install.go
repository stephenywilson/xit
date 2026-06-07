package opencodehook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type InstallResult struct {
	PluginPath       string
	AlreadyInstalled bool
}

// PluginPath returns the project-level plugin file path.
func PluginPath(projectDir string) string {
	return filepath.Join(projectDir, ".opencode", "plugins", "xit.ts")
}

// Install writes the XiT plugin to .opencode/plugins/xit.ts.
func Install(projectDir, home string, dryRun bool) (*InstallResult, error) {
	pluginPath := PluginPath(projectDir)
	alreadyInstalled := false
	if _, err := os.Stat(pluginPath); err == nil {
		alreadyInstalled = true
	}

	if dryRun {
		return &InstallResult{
			PluginPath:       pluginPath,
			AlreadyInstalled: alreadyInstalled,
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(pluginPath), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(pluginPath, []byte(PluginSource), 0644); err != nil {
		return nil, err
	}

	// Ensure events directory exists.
	if err := os.MkdirAll(filepath.Join(home, "opencode-hooks"), 0755); err != nil {
		return nil, err
	}

	return &InstallResult{
		PluginPath:       pluginPath,
		AlreadyInstalled: alreadyInstalled,
	}, nil
}

// Uninstall removes the XiT plugin from .opencode/plugins/xit.ts.
func Uninstall(projectDir string) error {
	pluginPath := PluginPath(projectDir)
	if _, err := os.Stat(pluginPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("XiT OpenCode plugin not found at %s", pluginPath)
		}
		return err
	}
	if err := os.Remove(pluginPath); err != nil {
		return err
	}
	return nil
}

type StatusResult struct {
	PluginPath  string
	Installed   bool
	HasEvents   bool
	EventsPath  string
}

// Status checks whether the XiT OpenCode plugin is installed.
func Status(projectDir, home string) (*StatusResult, error) {
	pluginPath := PluginPath(projectDir)
	_, err := os.Stat(pluginPath)
	installed := err == nil

	eventsPath := filepath.Join(home, "opencode-hooks", "events.jsonl")
	_, err = os.Stat(eventsPath)
	hasEvents := err == nil

	return &StatusResult{
		PluginPath: pluginPath,
		Installed:  installed,
		HasEvents:  hasEvents,
		EventsPath: eventsPath,
	}, nil
}

type StatsResult struct {
	Events      int
	Observed    int
	Rerouted    int
	Passthrough int
	Errors      int
	HasEvents   bool
}

// Stats reads opencode-hooks/events.jsonl and returns aggregated counts.
func Stats(home string) (*StatsResult, error) {
	result := &StatsResult{}
	path := filepath.Join(home, "opencode-hooks", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	lines := splitLines(string(data))
	for _, line := range lines {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		result.Events++
		result.HasEvents = true
		var rec map[string]interface{}
		if err := jsonUnmarshal([]byte(line), &rec); err != nil {
			result.Errors++
			continue
		}
		action, _ := rec["action"].(string)
		switch action {
		case "observe", "":
			result.Observed++
		case "reroute":
			result.Rerouted++
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

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	return string(trimSpaceBytes([]byte(s)))
}

func trimSpaceBytes(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

func jsonUnmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
