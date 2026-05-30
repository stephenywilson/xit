package kimihook

import (
	"os"
	"path/filepath"
)

func ProjectConfigPath() string {
	return ".kimi/config.toml"
}

func UserConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kimi", "config.toml")
}

func LegacyProjectConfigPath() string {
	return ".kimi/config.json"
}

func LegacyUserConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kimi", "config.json")
}

func ResolveConfigPath(scope string) string {
	if scope == "user" {
		return UserConfigPath()
	}
	return ProjectConfigPath()
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

// ResolveTurnStateHome returns the project and user home paths for turn state.
// The caller should prefer projectHome if it exists, otherwise fall back to userHome.
func ResolveTurnStateHome(cwd string) (projectHome, userHome string) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			cwd = "."
		}
	}
	projectHome = filepath.Join(cwd, ".xit")
	userHome = XiTHome()
	return
}
