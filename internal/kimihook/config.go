package kimihook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	xitMarker     = ".xit/hooks/kimi-pretooluse-shell.sh"
	xitTurnMarker = ".xit/hooks/kimi-turn-"
)

// --- Legacy JSON support (v0.2.9 and earlier) ---

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

func isXiTTurnCommand(cmd string) bool {
	// Matches either new explicit scripts or old generic script.
	return strings.Contains(cmd, xitTurnMarker) || strings.Contains(cmd, "kimi-turn-lifecycle.sh")
}

func HasXiTTurnHook(s *Settings, scriptPath string) bool {
	if s.Hooks == nil {
		return false
	}
	for eventName, entries := range s.Hooks {
		if eventName != "UserPromptSubmit" && eventName != "Stop" && eventName != "SessionStart" && eventName != "SessionEnd" {
			continue
		}
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				if isXiTTurnCommand(h.Command) {
					return true
				}
			}
		}
	}
	return false
}

func HasXiTEvent(s *Settings, eventName, scriptPath string) bool {
	if s.Hooks == nil {
		return false
	}
	for _, entry := range s.Hooks[eventName] {
		for _, h := range entry.Hooks {
			if isXiTTurnCommand(h.Command) {
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
	removed := false
	for eventName, entries := range s.Hooks {
		var newEntries []HookEntry
		for _, entry := range entries {
			var newHooks []HookDef
			for _, h := range entry.Hooks {
				if strings.Contains(h.Command, xitMarker) || h.Command == scriptPath || isXiTTurnCommand(h.Command) {
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
				s.Hooks[eventName] = newEntries
			} else {
				delete(s.Hooks, eventName)
			}
		}
	}
	return removed
}

func AddXiTHook(s *Settings, scriptPath string, turnScripts map[string]string) {
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

	// Add turn lifecycle hooks using explicit scripts.
	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionStart", "SessionEnd"} {
		path, ok := turnScripts[event]
		if !ok {
			continue
		}
		turnEntry := HookEntry{
			Hooks: []HookDef{
				{Type: "command", Command: path},
			},
		}
		// Avoid duplicates within the same event.
		var newEntries []HookEntry
		for _, e := range s.Hooks[event] {
			var keep []HookDef
			for _, h := range e.Hooks {
				if isXiTTurnCommand(h.Command) {
					continue
				}
				keep = append(keep, h)
			}
			if len(keep) > 0 {
				e.Hooks = keep
				newEntries = append(newEntries, e)
			}
		}
		s.Hooks[event] = append(newEntries, turnEntry)
	}
}

// --- TOML support (v0.2.10+) ---

func ReadToml(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func WriteToml(path string, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func HasXiTHookToml(content string) bool {
	return strings.Contains(content, xitMarker) || strings.Contains(content, xitTurnMarker) || strings.Contains(content, "kimi-turn-lifecycle.sh")
}

func HasXiTTurnHookToml(content string) bool {
	return strings.Contains(content, xitTurnMarker) || strings.Contains(content, "kimi-turn-lifecycle.sh")
}

func HasXiTEventToml(content, eventName string) bool {
	lines := strings.Split(content, "\n")
	inBlock := false
	blockEvent := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[[hooks]]" {
			inBlock = true
			blockEvent = ""
			continue
		}
		if inBlock {
			if strings.HasPrefix(trimmed, "event =") {
				parts := strings.SplitN(trimmed, "=", 2)
				if len(parts) == 2 {
					blockEvent = strings.Trim(strings.TrimSpace(parts[1]), `"`)
				}
			}
			if isXiTTurnCommand(trimmed) && blockEvent == eventName {
				return true
			}
			if trimmed == "[[hooks]]" {
				blockEvent = ""
			}
		}
	}
	return false
}

// RemoveXiTHookToml removes all [[hooks]] blocks that contain the XiT marker.
func RemoveXiTHookToml(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "[[hooks]]" {
			// Start of a hooks block; collect it to decide.
			var block []string
			block = append(block, line)
			j := i + 1
			for j < len(lines) {
				next := lines[j]
				if strings.TrimSpace(next) == "[[hooks]]" {
					break
				}
				block = append(block, next)
				j++
			}
			blockText := strings.Join(block, "\n")
			if !strings.Contains(blockText, xitMarker) && !isXiTTurnCommand(blockText) {
				result = append(result, block...)
			}
			i = j
			continue
		}
		result = append(result, line)
		i++
	}
	return strings.Join(result, "\n")
}

func parseTomlKeyValue(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	eq := strings.Index(trimmed, "=")
	if eq < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimmed[:eq])
	value = strings.TrimSpace(trimmed[eq+1:])
	return key, value, true
}

func isEmptyHooksArray(value string) bool {
	value = strings.TrimSpace(value)
	return value == "[]" || value == "[ ]"
}

func removeEmptyHooksArray(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		key, value, ok := parseTomlKeyValue(line)
		if ok && key == "hooks" && isEmptyHooksArray(value) {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func HasHooksConflictToml(content string) bool {
	hasInline := false
	hasArrayTable := false
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[[hooks]]" {
			hasArrayTable = true
		}
		key, value, ok := parseTomlKeyValue(line)
		if ok && key == "hooks" && isEmptyHooksArray(value) {
			hasInline = true
		}
	}
	return hasInline && hasArrayTable
}

func AddXiTHookToml(content string, scriptPath string, turnScripts map[string]string) (string, error) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		key, value, ok := parseTomlKeyValue(line)
		if ok && key == "hooks" && strings.HasPrefix(value, "[") && !isEmptyHooksArray(value) {
			return "", fmt.Errorf("inline non-empty hooks array is not supported; please convert to [[hooks]] manually")
		}
	}

	// Remove any existing XiT hooks first to avoid duplicates.
	content = RemoveXiTHookToml(content)
	// Remove empty inline hooks array to avoid conflict with [[hooks]].
	content = removeEmptyHooksArray(content)
	content = strings.TrimRight(content, "\n")

	var blocks []string
	blocks = append(blocks, fmt.Sprintf(`[[hooks]]
event = "PreToolUse"
matcher = "Shell"
command = %q
timeout = 5`, scriptPath))
	blocks = append(blocks, fmt.Sprintf(`[[hooks]]
event = "PreToolUse"
matcher = "Bash"
command = %q
timeout = 5`, scriptPath))

	for _, event := range []string{"UserPromptSubmit", "Stop", "SessionStart", "SessionEnd"} {
		path, ok := turnScripts[event]
		if !ok {
			continue
		}
		blocks = append(blocks, fmt.Sprintf(`[[hooks]]
event = %q
command = %q
timeout = 5`, event, path))
	}

	if content != "" {
		content += "\n\n"
	}
	content += strings.Join(blocks, "\n\n") + "\n"
	return content, nil
}

// ConfigFormat detects the format of the config file at the given path.
type ConfigFormat string

const (
	FormatTOML ConfigFormat = "toml"
	FormatJSON ConfigFormat = "json"
	FormatNone ConfigFormat = "none"
)

func DetectConfigFormat(path string) ConfigFormat {
	if _, err := os.Stat(path); err != nil {
		return FormatNone
	}
	if strings.HasSuffix(path, ".toml") {
		return FormatTOML
	}
	if strings.HasSuffix(path, ".json") {
		return FormatJSON
	}
	// Fallback: read first line and guess.
	data, err := os.ReadFile(path)
	if err != nil {
		return FormatNone
	}
	text := string(data)
	if strings.Contains(text, "[[hooks]]") || strings.Contains(text, "[hooks]") {
		return FormatTOML
	}
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		return FormatJSON
	}
	return FormatTOML
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
