package codexhook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// chatGPTAppPath is the standard macOS install location for the ChatGPT
// desktop app. Detection is read-only: it only stats the bundle and reads its
// own Info.plist metadata via `plutil` — it never launches the app, injects
// into its process, reads its signature, or touches any user data (chats,
// prompts, files) inside it.
const chatGPTAppPath = "/Applications/ChatGPT.app"

// ChatGPTAppInfo describes what was found on this machine for the ChatGPT
// desktop app.
type ChatGPTAppInfo struct {
	Installed bool
	BundleID  string
	Version   string
	Path      string
}

// IsChatGPTCodexApp reports whether the detected app is genuinely the merged
// ChatGPT/Codex desktop app (bundle id com.openai.codex) rather than merely
// something present at the conventional path.
func (i ChatGPTAppInfo) IsChatGPTCodexApp() bool {
	return i.Installed && i.BundleID == chatGPTDesktopBundleID
}

// DetectChatGPTApp performs a read-only check for the ChatGPT desktop app at
// its standard install path, extracting only Info.plist metadata (bundle id,
// version) via `plutil`. Never launches the app, modifies it, or reads any
// user data.
func DetectChatGPTApp() ChatGPTAppInfo {
	return DetectChatGPTAppAt(chatGPTAppPath)
}

// DetectChatGPTAppAt is DetectChatGPTApp against an arbitrary app bundle
// path, exported so tests can point it at a throwaway fixture bundle instead
// of the real /Applications/ChatGPT.app (which may not exist in CI).
func DetectChatGPTAppAt(appPath string) ChatGPTAppInfo {
	if _, err := os.Stat(appPath); err != nil {
		return ChatGPTAppInfo{Installed: false}
	}
	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	bundleID := plistString(plistPath, "CFBundleIdentifier")
	version := plistString(plistPath, "CFBundleShortVersionString")
	return ChatGPTAppInfo{
		Installed: true,
		BundleID:  bundleID,
		Version:   version,
		Path:      appPath,
	}
}

// plistString reads a single string value from a plist via `plutil -extract
// ... raw`, failing open to "" on any error (missing plist, missing key,
// plutil unavailable) — detection must never error out `xit doctor` or
// `xit chatgpt status`.
func plistString(plistPath, key string) string {
	out, err := exec.Command("/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plistPath).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// LocalPluginInstall describes what was found on disk for the local/personal
// XiT Codex Plugin — a read-only file-presence check. It never shells out to
// `codex plugin list` (fragile to parse, and codex may not be installed) and
// never claims to know the ChatGPT Desktop UI's enable/disable toggle state,
// which is not reflected in any file XiT can read.
type LocalPluginInstall struct {
	MarketplaceFound bool
	MarketplacePath  string
	PluginFound      bool
	PluginPath       string
}

// DetectLocalPluginInstall checks for the personal marketplace file
// (~/.agents/plugins/marketplace.json, referencing "xit") and the plugin
// content copied to ~/.codex/plugins/xit/.codex-plugin/plugin.json. Read-only.
func DetectLocalPluginInstall() LocalPluginInstall {
	home := codexUserConfigDir()
	marketplacePath := filepath.Join(home, ".agents", "plugins", "marketplace.json")
	pluginManifest := filepath.Join(home, ".codex", "plugins", "xit", ".codex-plugin", "plugin.json")

	result := LocalPluginInstall{MarketplacePath: marketplacePath, PluginPath: filepath.Dir(filepath.Dir(pluginManifest))}
	if data, err := os.ReadFile(marketplacePath); err == nil {
		result.MarketplaceFound = strings.Contains(string(data), `"name": "xit"`) || strings.Contains(string(data), `"name":"xit"`)
	}
	if _, err := os.Stat(pluginManifest); err == nil {
		result.PluginFound = true
	}
	return result
}
