package codexhook

import "os"

// Surface values distinguishing which Codex front-end hosts the process XiT
// is observing. adapter stays "codex" for all of these — only the surface
// breaks down further, so historical Codex telemetry stays continuous while
// the Dashboard can separately chart ChatGPT Desktop's Codex mode.
const (
	SurfaceCLI           = "codex_cli"
	SurfaceIDE           = "codex_ide"
	SurfaceChatGPTDesktop = "chatgpt_desktop_codex"
	// SurfaceShared is the safe fallback when Codex is confirmed but the
	// front-end cannot be reliably distinguished (e.g. a future Codex
	// front-end this detector doesn't know about yet). Never guessed —
	// used only when neither known ambient signal below matches.
	SurfaceShared = "codex_shared"

	// chatGPTDesktopBundleID is ChatGPT.app's real bundle identifier,
	// confirmed on-machine via:
	//   plutil -p /Applications/ChatGPT.app/Contents/Info.plist
	//     CFBundleIdentifier    => "com.openai.codex"
	//     CFBundleAlternateNames => ["Codex"]
	//     CFBundleDisplayName   => "ChatGPT"
	// ChatGPT Desktop's Codex mode runs the bundled `codex` binary at
	// Contents/Resources/codex as a direct child process of
	// Contents/MacOS/ChatGPT (confirmed via `ps -o pid,ppid,comm`).
	chatGPTDesktopBundleID = "com.openai.codex"
)

// DetectSurface identifies which Codex front-end is hosting this process,
// using ONLY ambient environment signals inherited from the parent process
// tree — the same mechanism XiT already uses for VSCODE_PID detection (see
// internal/vscodebridge.CurrentEnv/IsCodexVSCode). It never reads prompt
// text, tool output, file contents, or injects into any process.
//
// Priority:
//  1. VSCODE_PID present -> codex_ide. The "OpenAI ChatGPT" / Codex VS Code
//     extension spawns its own `codex ... app-server` subprocess inside VS
//     Code's process tree (confirmed on-machine via `ps aux` showing
//     .vscode/extensions/openai.chatgpt-*/bin/*/codex app-server), which
//     inherits VSCODE_PID exactly like any other VS Code child process.
//  2. __CFBundleIdentifier == "com.openai.codex" -> chatgpt_desktop_codex.
//     macOS/launchd injects __CFBundleIdentifier into any process descended
//     from a GUI app-bundle launch (confirmed on-machine: a plain terminal
//     shell carries its terminal emulator's own bundle id, e.g.
//     com.apple.Terminal; VS Code's integrated terminal carries
//     com.microsoft.VSCode). ChatGPT.app's embedded Codex agent process is a
//     direct child of Contents/MacOS/ChatGPT, so it — and anything it spawns
//     — carries ChatGPT's own bundle id, com.openai.codex. This is specific
//     to the ChatGPT desktop app, not any generic terminal.
//  3. Neither signal present -> codex_cli (plain terminal / non-GUI launch,
//     e.g. SSH, tmux, or a terminal emulator that isn't ChatGPT/VS Code).
//
// This never fabricates a result: if a future front-end sets neither signal,
// it safely falls through to codex_cli rather than mis-labeling anything as
// chatgpt_desktop_codex.
func DetectSurface() string {
	if os.Getenv("VSCODE_PID") != "" {
		return SurfaceIDE
	}
	if os.Getenv("__CFBundleIdentifier") == chatGPTDesktopBundleID {
		return SurfaceChatGPTDesktop
	}
	return SurfaceCLI
}
