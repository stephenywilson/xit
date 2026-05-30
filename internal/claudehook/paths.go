package claudehook

import (
	"os"
	"path/filepath"
)

func ProjectSettingsPath() string {
	return ".claude/settings.json"
}

func UserSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

func ResolveSettingsPath(scope string) string {
	if scope == "user" {
		return UserSettingsPath()
	}
	return ProjectSettingsPath()
}

func XiTHome() string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".xit")
	}
	return filepath.Join(home, ".xit")
}
