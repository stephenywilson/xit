package codexhook

import (
	"os"
	"path/filepath"
	"strings"
)

// eventStateKey maps a Codex hook event name to the snake_case key Codex
// uses in its ~/.codex/config.toml trust-state table, e.g.
// "[hooks.state.\"<hooks.json path>:pre_tool_use:0:0\"]".
func eventStateKey(eventName string) string {
	switch eventName {
	case "PreToolUse":
		return "pre_tool_use"
	case "PostToolUse":
		return "post_tool_use"
	case "UserPromptSubmit":
		return "user_prompt_submit"
	case "Stop":
		return "stop"
	default:
		return strings.ToLower(eventName)
	}
}

// TrustStatus reports, for the project's hooks.json, whether Codex has a
// recorded trust entry for each of the four lifecycle events in
// ~/.codex/config.toml. This is a read-only presence check (string search
// for the "[hooks.state.\"<path>:<event>:0:0\"]" table header Codex writes
// once a user approves a hook via /hooks) — it does NOT validate the
// recorded trusted_hash against the current file content, since Codex's
// exact hash input isn't documented; a hooks.json edited after the trust
// entry was recorded may still require the user to re-approve via /hooks
// even though a stale entry is present here.
type TrustStatus struct {
	ConfigPath      string
	AllRecorded     bool
	RecordedByEvent map[string]bool
}

// CheckTrustStatus reads ~/.codex/config.toml (never modifies it) and checks
// for a recorded trust-state entry per lifecycle event for projectPath's
// hooks.json.
func CheckTrustStatus(projectPath string) (*TrustStatus, error) {
	hooksPath := filepath.Join(projectPath, ".codex", "hooks.json")
	configPath := filepath.Join(codexUserConfigDir(), ".codex", "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &TrustStatus{ConfigPath: configPath, RecordedByEvent: map[string]bool{}}, nil
		}
		return nil, err
	}
	content := string(data)
	recorded := map[string]bool{}
	all := true
	for _, ev := range xitEvents() {
		key := "[hooks.state.\"" + hooksPath + ":" + eventStateKey(ev.Name) + ":0:0\"]"
		found := strings.Contains(content, key)
		recorded[ev.Name] = found
		if !found {
			all = false
		}
	}
	return &TrustStatus{ConfigPath: configPath, AllRecorded: all, RecordedByEvent: recorded}, nil
}
