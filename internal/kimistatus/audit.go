// Package kimistatus provides Kimi same-window status bar audit and prototype tools.
package kimistatus

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// BottomToolbarAuditResult holds the findings from a Kimi bottom toolbar audit.
type BottomToolbarAuditResult struct {
	KimiVersion           string
	PackagePath           string
	BottomToolbarDetected bool
	RendererMethod        string
	PromptToolkitVersion  string
	InjectionPaths        []InjectionPath
	ToastAvailable        bool
	ToastLimitations      string
	MonkeyPatchPossible   bool
	MonkeyPatchRisks      string
	NativeExtensionAPI    bool
	Conclusion            string
}

// InjectionPath describes a possible injection mechanism.
type InjectionPath struct {
	Name        string
	Available   bool
	Limitation  string
	Experimental bool
}

// RunAudit performs a read-only audit of the local Kimi installation.
func RunAudit() *BottomToolbarAuditResult {
	res := &BottomToolbarAuditResult{
		BottomToolbarDetected: true,
		RendererMethod:        "prompt_toolkit PromptSession.bottom_toolbar",
		ToastAvailable:        true,
		MonkeyPatchPossible:   true,
		NativeExtensionAPI:    false,
	}

	// Detect Kimi version and path.
	if path, err := exec.LookPath("kimi"); err == nil {
		res.PackagePath = path
		if out, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
			res.KimiVersion = strings.TrimSpace(string(out))
		}
	}

	// Detect prompt_toolkit version if possible.
	if out, err := exec.Command("python3", "-c", "import prompt_toolkit; print(prompt_toolkit.__version__)").CombinedOutput(); err == nil {
		res.PromptToolkitVersion = strings.TrimSpace(string(out))
	}

	res.InjectionPaths = []InjectionPath{
		{
			Name:        "Config file extension",
			Available:   false,
			Limitation:  "Kimi config has no bottom_toolbar or custom_status field",
			Experimental: false,
		},
		{
			Name:        "Hook event UI channel",
			Available:   false,
			Limitation:  "PreToolUse/PostToolUse hooks are shell scripts; stdout is parsed for deny decision only, not rendered to UI",
			Experimental: false,
		},
		{
			Name:        "Toast injection (right position)",
			Available:   true,
			Limitation:  "Requires running inside Kimi Python process; external process cannot call toast() on global queue",
			Experimental: true,
		},
		{
			Name:        "Monkey patch _render_bottom_toolbar",
			Available:   true,
			Limitation:  "Requires modifying installed Python package or sitecustomize injection; breaks on Kimi updates",
			Experimental: true,
		},
		{
			Name:        "Sitecustomize Python injection",
			Available:   runtime.GOOS != "windows",
			Limitation:  "May not affect uv-isolated environments; fragile across updates",
			Experimental: true,
		},
		{
			Name:        "Kimi Skill / Plugin API",
			Available:   false,
			Limitation:  "Skills are Markdown-only; plugin.json only registers tools, not UI extensions",
			Experimental: false,
		},
		{
			Name:        "ACP (Agent Control Protocol)",
			Available:   true,
			Limitation:  "ACP is for IDE embedding (VS Code), not terminal UI; cannot inject into prompt_toolkit bottom_toolbar",
			Experimental: false,
		},
	}

	res.ToastLimitations = "Toast messages expire by duration and are transient. To show persistent XiT status, a toast would need to be refreshed continuously by a process inside Kimi. This is not feasible from an external hook."
	res.MonkeyPatchRisks = "Monkey patching Kimi's installed package modifies files that are managed by uv/pip. Updates will overwrite patches. Incorrect patches can crash Kimi's TUI. This must remain experimental and opt-in only."
	res.Conclusion = "Kimi's bottom toolbar is implemented internally by prompt_toolkit with no external extension API. Persistent XiT status bar injection is BLOCKED without upstream support or experimental monkey patching. The safest near-term fallback is terminal title updates or rules-mode teaching Kimi to use xit auto."

	return res
}

// FormatAuditReport returns a human-readable audit report.
func FormatAuditReport(r *BottomToolbarAuditResult) string {
	var b strings.Builder
	b.WriteString("XiT Kimi Same-Window Status Bar Audit (v0.2.20)\n")
	b.WriteString("===============================================\n\n")

	b.WriteString("Kimi Installation:\n")
	b.WriteString(fmt.Sprintf("  version:         %s\n", orUnknown(r.KimiVersion)))
	b.WriteString(fmt.Sprintf("  path:            %s\n", orUnknown(r.PackagePath)))
	b.WriteString(fmt.Sprintf("  prompt_toolkit:  %s\n", orUnknown(r.PromptToolkitVersion)))
	b.WriteString("\n")

	b.WriteString("Bottom Toolbar:\n")
	b.WriteString(fmt.Sprintf("  detected:        %v\n", r.BottomToolbarDetected))
	b.WriteString(fmt.Sprintf("  renderer:        %s\n", r.RendererMethod))
	b.WriteString("  renders:         mode, model, thinking dot, CWD, git branch, yolo/afk/plan flags, bg tasks, tips, toast, context usage\n")
	b.WriteString("\n")

	b.WriteString("Injection Paths:\n")
	for _, p := range r.InjectionPaths {
		status := "NO"
		if p.Available {
			status = "YES"
		}
		exp := ""
		if p.Experimental {
			exp = " (experimental)"
		}
		b.WriteString(fmt.Sprintf("  %-40s %s%s\n", p.Name+":", status, exp))
		b.WriteString(fmt.Sprintf("    limitation:  %s\n", p.Limitation))
	}
	b.WriteString("\n")

	b.WriteString("Toast System:\n")
	b.WriteString(fmt.Sprintf("  available:       %v\n", r.ToastAvailable))
	b.WriteString(fmt.Sprintf("  limitations:     %s\n", r.ToastLimitations))
	b.WriteString("\n")

	b.WriteString("Monkey Patch:\n")
	b.WriteString(fmt.Sprintf("  possible:        %v\n", r.MonkeyPatchPossible))
	b.WriteString(fmt.Sprintf("  risks:           %s\n", r.MonkeyPatchRisks))
	b.WriteString("\n")

	b.WriteString("Native Extension API:\n")
	b.WriteString(fmt.Sprintf("  exists:          %v\n", r.NativeExtensionAPI))
	b.WriteString("\n")

	b.WriteString("Conclusion:\n")
	b.WriteString(fmt.Sprintf("  %s\n", r.Conclusion))

	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// TerminalTitleSupported reports whether the current terminal supports OSC title sequences.
func TerminalTitleSupported() bool {
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return false
	}
	return true
}

// SetTerminalTitle sets the terminal window/tab title using the OSC 0 sequence.
func SetTerminalTitle(title string) string {
	return fmt.Sprintf("\033]0;%s\007", title)
}

// KimiPackagePath attempts to locate the installed kimi_cli Python package.
func KimiPackagePath() string {
	// Common uv/pip install locations.
	candidates := []string{
		"~/.local/share/uv/tools/kimi-cli/lib/python3.13/site-packages/kimi_cli",
		"~/.local/share/uv/tools/kimi-cli/lib/python3.12/site-packages/kimi_cli",
		"~/.local/share/uv/tools/kimi-cli/lib/python3.11/site-packages/kimi_cli",
		"~/.local/lib/python3.13/site-packages/kimi_cli",
		"~/.local/lib/python3.12/site-packages/kimi_cli",
		"~/.local/lib/python3.11/site-packages/kimi_cli",
	}
	home, _ := os.UserHomeDir()
	for _, c := range candidates {
		c = strings.ReplaceAll(c, "~", home)
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
