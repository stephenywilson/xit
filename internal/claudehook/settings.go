package claudehook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Settings struct {
	Hooks map[string][]HookEntry `json:"hooks,omitempty"`
}

type HookEntry struct {
	Matcher string    `json:"matcher"`
	Hooks   []HookDef `json:"hooks"`
}

type HookDef struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

const xitMarker = ".xit/hooks/claude-pretooluse-bash.sh"

func ReadSettings(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Settings{}, nil
		}
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return &s, nil
}

func WriteSettings(path string, s *Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func BackupSettings(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	ts := time.Now().Format("20060102-150405")
	backup := path + ".xit-backup-" + ts
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(backup, data, 0644); err != nil {
		return "", err
	}
	return backup, nil
}

func HasXiTHook(s *Settings, scriptPath string) bool {
	if s.Hooks == nil {
		return false
	}
	for _, entry := range s.Hooks["PreToolUse"] {
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, xitMarker) || h.Command == scriptPath {
				return true
			}
		}
	}
	return false
}

func RemoveXiTHook(s *Settings, scriptPath string) bool {
	if s.Hooks == nil {
		return false
	}
	entries := s.Hooks["PreToolUse"]
	var newEntries []HookEntry
	removed := false
	for _, entry := range entries {
		var newHooks []HookDef
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, xitMarker) || h.Command == scriptPath {
				removed = true
				continue
			}
			newHooks = append(newHooks, h)
		}
		if len(newHooks) > 0 {
			entry.Hooks = newHooks
			newEntries = append(newEntries, entry)
		}
	}
	if removed {
		if len(newEntries) > 0 {
			s.Hooks["PreToolUse"] = newEntries
		} else {
			delete(s.Hooks, "PreToolUse")
		}
	}
	return removed
}

func AddXiTHook(s *Settings, scriptPath string) {
	if s.Hooks == nil {
		s.Hooks = make(map[string][]HookEntry)
	}
	RemoveXiTHook(s, scriptPath)

	entry := HookEntry{
		Matcher: "Bash",
		Hooks: []HookDef{
			{Type: "command", Command: scriptPath},
		},
	}
	s.Hooks["PreToolUse"] = append(s.Hooks["PreToolUse"], entry)
}
