package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stephenywilson/xit/internal/autoshim"
	"github.com/stephenywilson/xit/internal/autostate"
	"github.com/stephenywilson/xit/internal/claudehook"
	"github.com/stephenywilson/xit/internal/codexhook"
	"github.com/stephenywilson/xit/internal/config"
	"github.com/stephenywilson/xit/internal/doctor"
	"github.com/stephenywilson/xit/internal/filters"
	"github.com/stephenywilson/xit/internal/history"
	"github.com/stephenywilson/xit/internal/hitrate"
	"github.com/stephenywilson/xit/internal/impact"
	"github.com/stephenywilson/xit/internal/integrations"
	"github.com/stephenywilson/xit/internal/aiderrulesinstall"
	"github.com/stephenywilson/xit/internal/kimihook"
	"github.com/stephenywilson/xit/internal/kimirulesinstall"
	"github.com/stephenywilson/xit/internal/kimistatus"
	"github.com/stephenywilson/xit/internal/runner"
	"github.com/stephenywilson/xit/internal/session"
	"github.com/stephenywilson/xit/internal/shim"
	"os/exec"
)

const version = "0.2.43"

func main() {
	mode, rest := parseArgs(os.Args[1:])

	if len(rest) < 1 {
		if shouldInteractive() {
			showMainMenu(mode)
		} else {
			printHelp()
			os.Exit(1)
		}
		os.Exit(0)
	}

	arg := rest[0]
	switch arg {
	case "--version", "-v":
		fmt.Println("xit version", version)
		os.Exit(0)
	case "--help", "-h":
		printHelp()
		os.Exit(0)
	case "gain":
		if err := cmdGain(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "raw":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: xit raw <run-id>")
			os.Exit(1)
		}
		if err := cmdRaw(rest[1]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "doctor":
		fmt.Print(cmdDoctor(rest[1:]))
		os.Exit(0)
	case "init":
		if err := cmdInit(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "config":
		if err := cmdConfig(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "session":
		os.Exit(cmdSession(rest[1:], mode))
	case "auto":
		if err := cmdAuto(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "shim":
		if err := cmdShim(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "kimi", "claude", "codex", "gemini", "cursor", "antigravity", "aider":
		os.Exit(cmdWrapper(arg, rest[1:], mode))
	case "claude-hook":
		if err := cmdClaudeHook(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "kimi-hook":
		if err := cmdKimiHook(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "codex-hook":
		if err := cmdCodexHook(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "kimi-turn-status":
		os.Exit(cmdKimiTurnStatus(rest[1:]))
	case "kimi-instructions":
		cmdKimiInstructions()
		os.Exit(0)
	case "hook":
		if err := cmdHook(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	case "bench":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: xit bench compression [--run --yes]")
			os.Exit(1)
		}
		switch rest[1] {
		case "compression":
			os.Exit(cmdBenchCompression(rest[2:]))
		default:
			fmt.Fprintf(os.Stderr, "unknown bench subcommand: %s\n", rest[1])
			os.Exit(1)
		}
	case "uninstall":
		if err := cmdUninstall(rest[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	os.Exit(run(rest, mode))
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func shouldInteractive() bool {
	if os.Getenv("XIT_NONINTERACTIVE") == "1" || os.Getenv("CI") == "1" {
		return false
	}
	return isTerminal()
}

func showMainMenu(mode string) {
	reader := bufio.NewReader(os.Stdin)
	home := userXiTHome()
	cfg, _ := config.Load(home)
	if cfg == nil {
		cfg = config.Default(version)
	}
	for {
		fmt.Println("XiT / 吸T神功")
		adapters := integrations.AllAdapters()
		for i, a := range adapters {
			status := a.Detect()
			found := "not found"
			if status.Detected {
				found = "found"
			}
			ready := ""
			if t, ok := cfg.Targets[a.Name()]; ok && t.Enabled {
				ready = " [ready]"
			}
			fmt.Printf("%d. Start %-10s %s%s\n", i+1, a.DisplayName(), found, ready)
		}
		fmt.Printf("%d. Doctor\n", len(adapters)+1)
		fmt.Printf("%d. Init integrations\n", len(adapters)+2)
		fmt.Printf("%d. Gain report\n", len(adapters)+3)
		fmt.Println("0. Exit")
		fmt.Print("\nSelect: ")

		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		switch line {
		case "0":
			return
		case "1", "2", "3", "4", "5":
			idx, _ := strconv.Atoi(line)
			if idx >= 1 && idx <= len(adapters) {
				target := adapters[idx-1].Name()
				cmdWrapper(target, nil, mode)
			}
		case "6":
			fmt.Print(cmdDoctor(nil))
		case "7":
			showInitMenu(home, cfg)
		case "8":
			cmdGain()
		}
	}
}

func showInitMenu(home string, cfg *config.Config) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nXiT Init")
		fmt.Println("Choose integration target:")
		adapters := integrations.AllAdapters()
		for i, a := range adapters {
			status := a.Detect()
			found := "not found"
			if status.Detected {
				found = "found"
			}
			rec := string(status.RecommendedMethod)
			if rec == "official_hook" {
				rec = "official hook"
			}
			fmt.Printf("%d. %-15s %-10s recommended: %s\n", i+1, a.DisplayName(), found, rec)
		}
		fmt.Printf("%d. Manual only\n", len(adapters)+1)
		fmt.Println("0. Back")
		fmt.Print("\nSelect: ")

		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if line == "0" {
			return
		}

		idx, err := strconv.Atoi(line)
		if err != nil || idx < 1 || idx > len(adapters)+1 {
			fmt.Println("Invalid selection")
			continue
		}

		if idx == len(adapters)+1 {
			fmt.Println("\nManual mode: run commands through xit --mode agent <command>")
			continue
		}

		a := adapters[idx-1]
		plan := a.PlanInstall(home, cfg, "")
		fmt.Printf("\n%s", formatPlan(plan))

		if plan.CanInstall {
			fmt.Print("\nProceed with wrapper installation? (y/n): ")
			confirm, _ := reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "y" || confirm == "yes" {
				if err := a.Install(home, cfg, plan, true); err != nil {
					fmt.Fprintln(os.Stderr, "error:", err)
				} else {
					fmt.Printf("\nXiT initialized for %s.\n", a.DisplayName())
					fmt.Printf("Use: xit %s\n", a.Name())
				}
			}
		} else {
			fmt.Println("\nThis integration is not yet installable. Use the safe option shown above.")
		}
	}
}

func formatPlan(plan integrations.InstallPlan) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("XiT Install Plan: %s\n\n", plan.Target))
	if plan.Detected {
		b.WriteString("detected: yes\n")
		b.WriteString(fmt.Sprintf("path: %s\n", plan.Path))
	} else {
		b.WriteString("detected: no\n")
	}
	b.WriteString(fmt.Sprintf("recommended method: %s\n", plan.RecommendedMethod))
	if len(plan.SupportedMethods) > 0 {
		var ms []string
		for _, m := range plan.SupportedMethods {
			ms = append(ms, string(m))
		}
		b.WriteString(fmt.Sprintf("supported: %s\n", strings.Join(ms, ", ")))
	}
	b.WriteString("\nPlanned actions:\n")
	for i, act := range plan.Actions {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, act))
	}
	if plan.Note != "" {
		b.WriteString(fmt.Sprintf("\nNote: %s\n", plan.Note))
	}
	if plan.SafeOption != "" {
		b.WriteString(fmt.Sprintf("\nSafe option available now:\n  %s\n", plan.SafeOption))
	}
	return b.String()
}

func parseArgs(args []string) (string, []string) {
	mode := "human"
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--mode" && i+1 < len(args) {
			mode = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return mode, rest
}

func printHelp() {
	fmt.Println(`XiT 吸T神功 - Return wasted tokens as useful context.

Usage:
  xit [--mode human|agent|json] <command...>         Run a command and condense its output
  xit session [--quiet] [--mode <mode>] <cmd...>     Launch an interactive session with XiT tracking
  xit doctor                                         Check environment and AI CLI integration status
  xit doctor kimi --deep                             Deep diagnostics for Kimi hook config
  xit init [kimi|claude|codex|gemini|cursor]         Initialize XiT configuration
  xit init --all --dry-run                           Show install plan for all detected targets
  xit init <target> --method wrapper --yes           Enable wrapper for target
  xit init claude --method official_hook --yes       Install Claude Code official hook
  xit init kimi --method official_hook --yes         Install Kimi observe hook (beta)
  xit init kimi --method official_hook --scope user --yes  Install Kimi hook to user scope
  xit config                                         Show XiT configuration
  xit shim status                                    Show shim status
  xit shim install <target> --yes [--takeover]       Install a shim for target
  xit shim remove <target>                           Remove a shim for target
  xit hook status claude                             Show Claude hook status
  xit hook status kimi                               Show Kimi hook status
  xit hook status kimi --scope user                  Show Kimi hook status (user scope)
  xit hook test kimi                                 Self-test Kimi hook (local only)
  xit hook enable-reroute claude --yes               Enable Claude safe reroute
  xit hook disable-reroute claude --yes              Disable Claude safe reroute
  xit hook stats claude                              Show Claude hook statistics
  xit hook hitrate claude                            Show Claude routing hit rate audit
  xit hook hitrate claude --json                     Show Claude routing hit rate as JSON
  xit hook hitrate claude --last 2h                  Show hit rate for last 2 hours
  xit hook enable-reroute kimi --yes                 Enable Kimi safe reroute
  xit hook disable-reroute kimi --yes                Disable Kimi safe reroute
  xit hook status-style kimi compact --yes           Set Kimi inline status to compact
  xit hook status-style kimi detailed --yes          Set Kimi inline status to detailed
  xit hook stats kimi                                Show Kimi hook statistics
  xit uninstall claude --method official_hook --yes  Uninstall Claude official hook
  xit uninstall kimi --method official_hook --yes    Uninstall Kimi official hook
  xit uninstall kimi --method official_hook --scope user --yes  Uninstall Kimi user-scope hook
  xit kimi instructions                              Show Kimi hook discovery instructions
  xit kimi response-schema                           Show Kimi hook response schema discovery
  xit kimi status-bar-audit                          Audit same-window status bar feasibility
  xit kimi status-bar-audit --deep                   Deep audit with Kimi source findings
  xit kimi status-prototype                          Show status bar prototype options
  xit kimi status-prototype --audit                  Run feasibility audit (same as status-bar-audit)
  xit kimi status-prototype --title                  Set terminal title to XiT status
  xit kimi status-prototype --dry-run-patch          Generate experimental bottom toolbar patch script
  xit kimi doctor                                    Run Kimi integration health check
  xit kimi status-patch status                       Check if Kimi bottom toolbar can be patched
  xit kimi status-patch preview                      Show toolbar preview and rotation candidates
  xit kimi status-patch dry-run                      Show patch plan without modifying files
  xit kimi status-patch check-update                 Read-only check if patch is still valid
  xit kimi status-patch install --yes --accept-risk  Install bottom toolbar monkey patch (EXPERIMENTAL)
  xit kimi status-patch uninstall --yes              Restore original Kimi files from backup
  xit kimi turn-status                               Show Kimi turn lifecycle state
  xit kimi turn-status --json                        Show turn state as JSON
  xit bench compression                              Show XiT compression quality benchmark
  xit bench compression --run --yes                  Run read-only commands and benchmark compression
  xit kimi benchmark                                 Show XiT compression benchmark (Kimi alias)
  xit kimi rules                                     Show rules mode overview and install instructions
  xit kimi rules preview                             Preview the SKILL.md content that would be installed
  xit kimi rules install --scope user --yes          Install XiT skill into Kimi (user scope)
  xit kimi rules install --scope project --yes       Install XiT skill into Kimi (project scope)
  xit kimi rules status --scope user                 Check if XiT skill is installed
  xit kimi rules uninstall --scope user --yes        Remove XiT skill from Kimi
  xit kimi rules dogfood                             Print copy-paste prompt to verify rules are active
  xit aider rules                                    Show Aider rules mode overview
  xit aider rules preview                            Preview the rules content that would be installed
  xit aider rules install --scope project --yes      Install XiT rules into Aider (project scope)
  xit aider rules status --scope project             Check if XiT Aider rules are installed
  xit aider rules uninstall --scope project --yes    Remove XiT Aider rules
  xit session [--no-auto-shims] <cmd...>             Launch session with auto command shims
  xit kimi [--unsafe-pty] [--no-auto-shims]          Launch Kimi with XiT wrapper (unsafe)
  xit claude statusline                              Output one-line Claude Code status bar text
  xit claude statusline --json                       Output status bar data as JSON
  xit claude statusline install --scope project-local --yes  Install statusLine into .claude/settings.local.json
  xit claude statusline status                       Show Claude statusLine config status
  xit claude statusline uninstall --yes              Remove XiT statusLine from .claude/settings.local.json
  xit claude                                         Launch Claude with XiT wrapper
  xit codex                                          Launch Codex with XiT wrapper
  xit gain                                           Show saved token statistics
  xit raw <run-id>                                   Display a saved raw log
  xit --version                                      Show version
  xit --help                                         Show this help

Examples:
  xit git status
  xit --mode agent git diff
  xit --mode json npm test
  xit rg "func main"
  xit session bash
  xit session --quiet --mode agent claude`)
}

func xitHome() string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	return filepath.Join(wd, ".xit")
}

func userXiTHome() string {
	if v := os.Getenv("XIT_HOME"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".xit")
	}
	return filepath.Join(home, ".xit")
}

func run(args []string, mode string) int {
	home := xitHome()
	xh := &runner.XitHome{Path: home}
	if err := xh.Ensure(); err != nil {
		fmt.Fprintln(os.Stderr, "xit: cannot create home:", err)
		return 1
	}

	res, err := runner.Run(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: run error:", err)
		return 1
	}

	if err := xh.SaveRaw(args, res); err != nil {
		fmt.Fprintln(os.Stderr, "xit: save raw log error:", err)
		return 1
	}

	disp := filters.NewDispatcher()
	summary, err := disp.Dispatch(args, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: filter error:", err)
		fmt.Fprintln(os.Stderr, "--- stdout ---")
		fmt.Fprintln(os.Stderr, string(res.Stdout))
		fmt.Fprintln(os.Stderr, "--- stderr ---")
		fmt.Fprintln(os.Stderr, string(res.Stderr))
		return res.ExitCode
	}

	fmt.Print(summary.Render(mode))

	if err := disp.WriteHistory(home, args, res, summary); err != nil {
		fmt.Fprintln(os.Stderr, "xit: history error:", err)
	}

	if sessionDir := os.Getenv("XIT_SESSION_DIR"); sessionDir != "" {
		if err := disp.WriteHistory(sessionDir, args, res, summary); err != nil {
			fmt.Fprintln(os.Stderr, "xit: session history error:", err)
		}
	}

	return res.ExitCode
}

func cmdGain() error {
	g, err := history.ComputeGain(xitHome())
	if err != nil {
		return err
	}
	fmt.Print(history.FormatGain(g))
	return nil
}

func cmdRaw(runID string) error {
	path := filepath.Join(xitHome(), "runs", runID)
	if !strings.HasSuffix(path, ".raw.log") {
		path += ".raw.log"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func cmdDoctor(args []string) string {
	home := userXiTHome()
	r := doctor.Run(home)
	r.Version = version

	var cfg *config.Config
	if config.Exists(home) {
		cfg, _ = config.Load(home)
	}
	if cfg == nil {
		cfg = config.Default(version)
	}

	// If a target is specified, show per-target compatibility.
	if len(args) > 0 {
		target := args[0]
		if target == "kimi" && hasDeepFlag(args) {
			return cmdDoctorKimiDeep(home, cfg)
		}
		return cmdDoctorTarget(target, home, cfg)
	}

	var b strings.Builder
	b.WriteString("XiT Doctor\n\n")
	b.WriteString(fmt.Sprintf("version:      %s\n", r.Version))
	b.WriteString(fmt.Sprintf("xit path:     %s\n", r.XitPath))
	b.WriteString(fmt.Sprintf("shell:        %s\n", r.Shell))
	b.WriteString(fmt.Sprintf("os/arch:      %s\n", r.OSArch))
	b.WriteString(fmt.Sprintf("xit home:     %s\n", r.XiTHome))
	if r.ConfigOK {
		b.WriteString(fmt.Sprintf("config:       found\n"))
	} else {
		b.WriteString("config:       missing\n")
	}
	b.WriteString("\nAI CLI integrations:\n\n")

	for _, result := range integrations.DoctorAll(cfg) {
		b.WriteString(fmt.Sprintf("%s:\n", result.Name))
		b.WriteString(fmt.Sprintf("  command:     %s\n", result.Command))
		if result.Detected {
			b.WriteString(fmt.Sprintf("  detected:    yes\n"))
			b.WriteString(fmt.Sprintf("  path:        %s\n", result.Path))
		} else {
			b.WriteString(fmt.Sprintf("  detected:    no\n"))
		}
		b.WriteString(fmt.Sprintf("  recommended: %s\n", result.Recommended))
		b.WriteString(fmt.Sprintf("  installed:   %s\n", result.Installed))
		if len(result.Fallback) > 0 {
			var fs []string
			for _, m := range result.Fallback {
				fs = append(fs, string(m))
			}
			b.WriteString(fmt.Sprintf("  fallback:    %s\n", strings.Join(fs, ", ")))
		}
		if result.Note != "" {
			b.WriteString(fmt.Sprintf("  note:        %s\n", result.Note))
		}
		b.WriteString("\n")
	}

	b.WriteString("Capabilities:\n\n")
	b.WriteString("* manual compression: ready\n")
	b.WriteString("* session mode:       ready\n")
	b.WriteString("* wrapper mode:       available\n")
	autoHookStatus := "not installed"
	if cfg.Targets["claude"].Integration == "official_hook" {
		autoHookStatus = "claude official_hook"
	}
	b.WriteString(fmt.Sprintf("* auto hook:          %s\n", autoHookStatus))
	if !r.ConfigOK {
		b.WriteString("\nRecommendation:\n")
		b.WriteString("Run: xit init\n")
	}
	return b.String()
}

func cmdDoctorTarget(target, home string, cfg *config.Config) string {
	var b strings.Builder
	switch target {
	case "kimi":
		b.WriteString("XiT Kimi Compatibility\n\n")
		path := config.DetectPath("kimi")
		if path == "" {
			b.WriteString("kimi path: not found in PATH\n")
			b.WriteString("detected:  no\n")
		} else {
			b.WriteString(fmt.Sprintf("kimi path: %s\n", path))
			b.WriteString("detected:  yes\n")
			b.WriteString("launcher:  likely Python script\n")
			b.WriteString("tui:       full-screen interactive\n")
		}
		b.WriteString("hooks:                 beta\n")
		b.WriteString("config candidates:\n")
		b.WriteString("  - .kimi/config.toml\n")
		b.WriteString("  - ~/.kimi/config.toml\n")
		b.WriteString("  - ~/.kimi-code/config.toml\n")
		b.WriteString("current selected:      .kimi/config.toml\n")
		b.WriteString("matcher compatibility: Shell + Bash\n")
		b.WriteString("takeover:              disabled by default for TUI safety\n")
		b.WriteString("pty wrapper:           unsafe\n")
		b.WriteString("recommended:           manual\n\n")
		b.WriteString("Safe usage:\n")
		b.WriteString("  kimi\n")
		b.WriteString("  xit --mode agent go test -v ./...\n")
		b.WriteString("  xit --mode agent git diff\n\n")
		b.WriteString("Kimi hook beta:\n")
		b.WriteString("  xit init kimi --method official_hook --scope project --yes\n")
		b.WriteString("  xit hook status kimi\n\n")
		b.WriteString("Rollback:\n")
		b.WriteString("  xit shim remove kimi\n")
		b.WriteString("  xit uninstall kimi --method official_hook --yes\n\n")
		b.WriteString("Advanced / unsafe:\n")
		b.WriteString("  xit kimi --unsafe-pty\n")
		b.WriteString("  xit shim install kimi --yes --takeover --force-unsafe-tui\n")
	default:
		b.WriteString(fmt.Sprintf("XiT %s Compatibility\n\n", target))
		b.WriteString("Use: xit doctor\n")
	}
	return b.String()
}

func cmdDoctorKimiDeep(home string, cfg *config.Config) string {
	var b strings.Builder
	b.WriteString("XiT Kimi Health Check\n")
	b.WriteString("=====================\n\n")

	// Kimi binary
	kimiPath := config.DetectPath("kimi")
	kimiVersion := ""
	if kimiPath != "" {
		if out, err := exec.Command(kimiPath, "--version").CombinedOutput(); err == nil {
			kimiVersion = strings.TrimSpace(string(out))
		}
	}

	pkgDir, _ := kimistatus.LocateKimiPackage()
	if pkgDir == "" {
		pkgDir = kimistatus.KimiPackagePath()
	}

	b.WriteString("Kimi:\n")
	if kimiPath == "" {
		b.WriteString("  binary:   not found\n")
	} else {
		b.WriteString(fmt.Sprintf("  binary:   %s\n", kimiPath))
	}
	b.WriteString(fmt.Sprintf("  version:  %s\n", orUnknown(kimiVersion)))
	b.WriteString(fmt.Sprintf("  package:  %s\n", orUnknown(pkgDir)))

	// Config
	selectedConfig := kimihook.ResolveConfigPath("project")
	b.WriteString(fmt.Sprintf("  config:   %s\n", selectedConfig))
	b.WriteString("\n")

	// Rules mode
	rulesSt, _ := kimirulesinstall.Status("user")
	b.WriteString("Rules mode:\n")
	if rulesSt != nil && rulesSt.Installed {
		b.WriteString("  skill:    installed\n")
		b.WriteString(fmt.Sprintf("  path:     %s\n", rulesSt.SkillPath))
		b.WriteString("  active:   yes\n")
	} else {
		b.WriteString("  skill:    not installed\n")
		b.WriteString("  active:   no\n")
	}
	b.WriteString("\n")

	// Hook observe
	hookSt, _ := kimihook.Status(selectedConfig, home)
	b.WriteString("Hook observe:\n")
	if hookSt != nil {
		b.WriteString(fmt.Sprintf("  installed: %v\n", hookSt.Installed))
		b.WriteString(fmt.Sprintf("  scope:     %s\n", hookSt.ConfigPath))
		b.WriteString(fmt.Sprintf("  config:    %s\n", hookSt.ConfigPath))
		b.WriteString(fmt.Sprintf("  mode:      %s\n", hookSt.Mode))
		b.WriteString(fmt.Sprintf("  reroute:   %v\n", hookSt.Reroute))
	} else {
		b.WriteString("  installed: false\n")
	}
	b.WriteString("\n")

	// Status patch
	patchRes := &kimistatus.PatchCheckResult{}
	if pkgDir != "" {
		patchRes = kimistatus.CheckPatchable(pkgDir)
	}
	preview := kimistatus.ComputeToolbarPreview(home)
	backup := ""
	if patchRes.PromptPyPath != "" {
		backup = kimistatus.FindBackup(patchRes.PromptPyPath)
	}
	b.WriteString("Status patch:\n")
	b.WriteString(fmt.Sprintf("  installed:  %v\n", patchRes.Installed))
	if patchRes.PromptPyPath != "" {
		b.WriteString(fmt.Sprintf("  target:     %s\n", patchRes.PromptPyPath))
	}
	if backup != "" {
		b.WriteString(fmt.Sprintf("  backup:     %s\n", backup))
	} else {
		b.WriteString("  backup:     none\n")
	}
	b.WriteString(fmt.Sprintf("  toolbar_preview: %s\n", preview.Preview))
	b.WriteString("  risk:       high\n")
	b.WriteString("  patch_type: monkey_patch\n")
	b.WriteString("\n")

	// Compression
	g, _ := history.ComputeGain(home)
	b.WriteString("Compression:\n")
	b.WriteString(fmt.Sprintf("  commands_condensed:   %d\n", g.TotalCommands))
	b.WriteString(fmt.Sprintf("  total_raw_bytes:      %d\n", g.TotalRawBytes))
	b.WriteString(fmt.Sprintf("  total_summary_bytes:  %d\n", g.TotalSummaryBytes))
	b.WriteString(fmt.Sprintf("  estimated_reduction:  %.1f%%\n", g.EstimatedReduction*100))
	if len(g.TopCommands) > 0 {
		b.WriteString("  latest_raw_logs:      yes\n")
	} else {
		b.WriteString("  latest_raw_logs:      no\n")
	}
	b.WriteString("\n")

	// Metrics
	m, _ := history.ComputeSessionMetrics(home, 2*time.Hour)
	b.WriteString("Metrics:\n")
	b.WriteString(fmt.Sprintf("  current_session:\n"))
	b.WriteString(fmt.Sprintf("    auto_commands:  %d\n", m.CurrentSession.AutoCommands))
	b.WriteString(fmt.Sprintf("    saved_bytes:    %d\n", m.CurrentSession.SavedBytes))
	b.WriteString(fmt.Sprintf("    saved_tokens:   %d\n", m.CurrentSession.SavedBytes/4))
	b.WriteString(fmt.Sprintf("    token_method:   saved_bytes / 4\n"))
	b.WriteString(fmt.Sprintf("    reduction:      %.1f%%\n", m.CurrentSession.Reduction*100))
	b.WriteString(fmt.Sprintf("  lifetime:\n"))
	b.WriteString(fmt.Sprintf("    auto_commands:  %d\n", m.Lifetime.AutoCommands))
	b.WriteString(fmt.Sprintf("    saved_bytes:    %d\n", m.Lifetime.SavedBytes))
	b.WriteString(fmt.Sprintf("    saved_tokens:   %d\n", m.Lifetime.SavedBytes/4))
	b.WriteString(fmt.Sprintf("    token_method:   saved_bytes / 4\n"))
	b.WriteString(fmt.Sprintf("  token_accuracy:   estimated only, not Kimi context tokens\n"))
	b.WriteString("\n")

	// Safety
	b.WriteString("Safety:\n")
	b.WriteString("  xit_kimi_wrapper:   blocked\n")
	b.WriteString("  takeover:           refused_by_default\n")
	b.WriteString("  rollback:\n")
	b.WriteString("    xit kimi status-patch uninstall --yes\n")
	b.WriteString("\n")

	// Verdict
	b.WriteString("Verdict:\n")
	b.WriteString("  Kimi integration functional prototype: PASS\n")
	b.WriteString("  Official integration: NO\n")
	if rulesSt != nil && rulesSt.Installed && g.TotalCommands > 0 {
		b.WriteString("\nXiT is actively condensing output for Kimi.\n")
		b.WriteString("Use 'xit kimi status-patch status' for patch details.\n")
		b.WriteString("Use 'xit kimi benchmark' for compression stats.\n")
	}

	return b.String()
}

func padPath(p string) string {
	const width = 35
	if len(p) >= width {
		return p + " "
	}
	return p + strings.Repeat(" ", width-len(p))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func cmdInit(args []string) error {
	home := userXiTHome()
	force := false
	dryRun := false
	all := false
	yes := false
	var method string
	var target string
	var scope string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--force" {
			force = true
			continue
		}
		if a == "--dry-run" {
			dryRun = true
			continue
		}
		if a == "--all" {
			all = true
			continue
		}
		if a == "--yes" {
			yes = true
			continue
		}
		if a == "--method" && i+1 < len(args) {
			method = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--method=") {
			method = strings.TrimPrefix(a, "--method=")
			continue
		}
		if a == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(a, "--scope=") {
			scope = strings.TrimPrefix(a, "--scope=")
			continue
		}
		if target == "" && !strings.HasPrefix(a, "-") {
			target = a
		}
	}

	cfgExists := config.Exists(home)
	var cfg *config.Config
	if cfgExists && !force {
		var err error
		cfg, err = config.Load(home)
		if err != nil {
			cfg = config.Default(version)
		}
	} else {
		cfg = config.Default(version)
	}

	// No target, not --all
	if target == "" && !all {
		if shouldInteractive() {
			showInitMenu(home, cfg)
			return nil
		}
		if cfgExists && !force {
			return fmt.Errorf("config already exists at %s. Use --force to overwrite", config.Path(home))
		}
		for name := range cfg.Targets {
			if p := config.DetectPath(name); p != "" {
				t := cfg.Targets[name]
				t.Path = p
				cfg.Targets[name] = t
			}
		}
		if err := config.Save(home, cfg); err != nil {
			return err
		}
		fmt.Printf("XiT initialized.\n\nconfig:   %s\ntelemetry: false\n\nRun one of the following to enable a wrapper:\n  xit init kimi\n  xit init claude\n  xit init codex\n", config.Path(home))
		return nil
	}

	if all {
		if dryRun {
			fmt.Println("XiT Install Plan: All detected targets")
		}
		detected := 0
		for _, a := range integrations.AllAdapters() {
			status := a.Detect()
			if !status.Detected {
				continue
			}
			detected++
			plan := a.PlanInstall(home, cfg, method)
			if dryRun {
				fmt.Print(formatPlan(plan))
				fmt.Println()
				continue
			}
			if plan.CanInstall && yes {
				if err := a.Install(home, cfg, plan, true); err == nil {
					fmt.Printf("Installed %s wrapper\n", a.DisplayName())
				} else {
					fmt.Fprintf(os.Stderr, "error installing %s: %v\n", a.Name(), err)
				}
			} else if !plan.CanInstall {
				fmt.Printf("Skipping %s: %s\n", a.DisplayName(), plan.Note)
			} else {
				fmt.Printf("Skipping %s: requires --yes to install\n", a.DisplayName())
			}
		}
		if dryRun && detected == 0 {
			fmt.Println("No AI CLI tools detected in PATH.")
		}
		if !dryRun {
			fmt.Printf("Done. Run 'xit doctor' to see integration status.\n")
		}
		return nil
	}

	// Single target
	a, ok := integrations.Registry[target]
	if !ok {
		return fmt.Errorf("unknown target: %s", target)
	}

	if method == "" {
		method = "wrapper"
	}

	plan := a.PlanInstall(home, cfg, method)
	plan.Scope = scope

	if dryRun {
		fmt.Print(formatPlan(plan))
		return nil
	}

	if plan.Path == "" {
		return fmt.Errorf("%s not found in PATH", target)
	}

	if !plan.CanInstall {
		fmt.Print(formatPlan(plan))
		return fmt.Errorf("cannot install %s with method %s in this build", target, plan.SelectedMethod)
	}

	if !yes {
		fmt.Print(formatPlan(plan))
		return fmt.Errorf("install requires --yes to confirm. Run: xit init %s --method %s --yes", target, plan.SelectedMethod)
	}

	if err := a.Install(home, cfg, plan, true); err != nil {
		return err
	}

	fmt.Printf("XiT initialized for %s.\n\n", a.DisplayName())
	fmt.Printf("%s path:       %s\n", target, plan.Path)
	fmt.Printf("integration:  %s\n", plan.SelectedMethod)
	fmt.Printf("config:       %s\n\n", config.Path(home))
	fmt.Printf("Use:\n  xit %s\n\n", target)
	fmt.Println("Inside the AI CLI, ask it to run long terminal commands through:")
	fmt.Println("  xit --mode agent go test -v ./...")
	fmt.Println("  xit --mode agent git diff")
	fmt.Println("  xit --mode agent grep -r \"func\" --include=\"*.go\" .")
	fmt.Println("\nCurrent limitation:")
	fmt.Println("XiT wrapper starts the AI CLI visibly and records the session,")
	fmt.Println("but does not automatically intercept every internal tool call yet.")
	return nil
}

func cmdConfig() error {
	home := userXiTHome()
	if !config.Exists(home) {
		return fmt.Errorf("Config not found. Run: xit init")
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	fmt.Print(cfg.FormatSummary(home))
	return nil
}

func writeAutoState(statePath string, state map[string]interface{}) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	dir := filepath.Dir(statePath)
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(statePath, data, 0644)
}

func cmdAuto(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: xit auto <tool> [args...]")
	}
	tool := args[0]
	toolArgs := args[1:]

	// State file setup (fail-open).
	home := xitHome()
	statePath := filepath.Join(home, "state", "current.json")
	cmdStr := strings.Join(append([]string{tool}, toolArgs...), " ")
	startedAt := time.Now().UTC().Format(time.RFC3339)

	writeAutoState(statePath, map[string]interface{}{
		"status":      "running",
		"command":     cmdStr,
		"started_at":  startedAt,
		"pid":         os.Getpid(),
	})

	// Find original binary path, avoiding recursion.
	origPath := autoshim.ResolveOriginal(tool)
	if origPath == "" {
		writeAutoState(statePath, map[string]interface{}{
			"status":       "failed",
			"command":      cmdStr,
			"exit_code":    -1,
			"saved_bytes":  0,
			"raw_log":      "",
			"finished_at":  time.Now().UTC().Format(time.RFC3339),
		})
		return fmt.Errorf("cannot find original %s path", tool)
	}

	// Build the actual command to run.
	actualArgs := append([]string{origPath}, toolArgs...)
	res, err := runner.Run(actualArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: auto run error:", err)
		writeAutoState(statePath, map[string]interface{}{
			"status":       "failed",
			"command":      cmdStr,
			"exit_code":    -1,
			"saved_bytes":  0,
			"raw_log":      "",
			"finished_at":  time.Now().UTC().Format(time.RFC3339),
		})
		return err
	}

	// Decide whether to compress or passthrough.
	rawBytes := len(res.Stdout) + len(res.Stderr)
	shouldCompress := autoshim.ShouldCompress(tool, toolArgs, rawBytes, res.ExitCode)

	if !shouldCompress {
		// Passthrough: print raw stdout/stderr exactly as-is.
		os.Stdout.Write(res.Stdout)
		os.Stderr.Write(res.Stderr)
		writeAutoState(statePath, map[string]interface{}{
			"status":       "completed",
			"command":      cmdStr,
			"exit_code":    res.ExitCode,
			"saved_bytes":  0,
			"raw_log":      res.RawLogPath,
			"finished_at":  time.Now().UTC().Format(time.RFC3339),
		})
		return nil
	}

	// Compression path.
	xh := &runner.XitHome{Path: home}
	_ = xh.Ensure()
	_ = xh.SaveRaw(actualArgs, res)

	disp := filters.NewDispatcher()
	summary, err := disp.Dispatch(actualArgs, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: auto filter error:", err)
		os.Stdout.Write(res.Stdout)
		os.Stderr.Write(res.Stderr)
		writeAutoState(statePath, map[string]interface{}{
			"status":       "completed",
			"command":      cmdStr,
			"exit_code":    res.ExitCode,
			"saved_bytes":  0,
			"raw_log":      res.RawLogPath,
			"finished_at":  time.Now().UTC().Format(time.RFC3339),
		})
		return nil
	}

	savedBytes := rawBytes - len([]byte(summary.Render("human")))
	if savedBytes < 0 {
		savedBytes = 0
	}

	// Render auto summary.
	fmt.Println("XiT Auto Summary")
	fmt.Printf("command: %s %s\n", tool, strings.Join(toolArgs, " "))
	fmt.Printf("exit_code: %d\n", res.ExitCode)
	fmt.Printf("estimated_reduction: %.0f%%\n", summary.EstimatedReduction*100)
	fmt.Printf("saved_tokens: ~%s\n", formatTokenCount(savedBytes/4))
	fmt.Printf("raw_log: %s\n", res.RawLogPath)
	fmt.Println()
	fmt.Print(summary.Render("agent"))

	// Write history.
	_ = disp.WriteHistory(home, actualArgs, res, summary)
	if sessionDir := os.Getenv("XIT_SESSION_DIR"); sessionDir != "" {
		_ = disp.WriteHistory(sessionDir, actualArgs, res, summary)
	}
	writeAutoState(statePath, map[string]interface{}{
		"status":       "completed",
		"command":      cmdStr,
		"exit_code":    res.ExitCode,
		"saved_bytes":  savedBytes,
		"raw_log":      res.RawLogPath,
		"finished_at":  time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

func cmdShim(args []string) error {
	home := userXiTHome()
	if len(args) < 1 {
		return fmt.Errorf("usage: xit shim status | xit shim install <target> --yes | xit shim remove <target>")
	}
	sub := args[0]
	switch sub {
	case "status":
		if !config.Exists(home) {
			return fmt.Errorf("Config not found. Run: xit init")
		}
		cfg, err := config.Load(home)
		if err != nil {
			return err
		}
		fmt.Print(shim.Status(home, cfg))
		return nil
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: xit shim install <target> --yes [--takeover] [--force-unsafe-tui]")
		}
		target := args[1]
		yes := false
		takeover := false
		forceUnsafeTUI := false
		for _, a := range args[2:] {
			if a == "--yes" {
				yes = true
			}
			if a == "--takeover" {
				takeover = true
			}
			if a == "--force-unsafe-tui" {
				forceUnsafeTUI = true
			}
		}
		if !config.Exists(home) {
			return fmt.Errorf("Config not found. Run: xit init %s", target)
		}
		cfg, err := config.Load(home)
		if err != nil {
			return err
		}
		// Kimi takeover guard: block takeover by default due to TUI incompatibility.
		if target == "kimi" && takeover && !forceUnsafeTUI {
			return fmt.Errorf(`Kimi takeover is disabled by default.

Reason:
Kimi Code CLI uses a full-screen interactive TUI that currently does not work reliably through XiT's PTY wrapper.

Observed risk:
- input box may stop submitting
- CPR/cursor-position warnings may appear
- interactive session may become unusable

Recommended:
  Use manual XiT compression:
    xit --mode agent go test -v ./...
    xit --mode agent git diff

Or use wrapper without takeover only for compatibility testing:
    xit kimi --no-auto-shims

To force takeover for development only:
    xit shim install kimi --yes --takeover --force-unsafe-tui`)
		}
		if err := shim.Install(home, cfg, target, yes, takeover); err != nil {
			return err
		}
		shimPath, _ := os.UserHomeDir()
		shimPath = filepath.Join(shimPath, ".local", "bin", target)
		if t, ok := cfg.Targets[target]; ok && t.ShimPath != "" {
			shimPath = t.ShimPath
		}
		original := ""
		if t, ok := cfg.Targets[target]; ok {
			original = t.OriginalPath
			if original == "" {
				original = t.Path
			}
		}
		fmt.Printf("XiT shim installed for %s.\n\n", strings.Title(target))
		fmt.Printf("Original %s: %s\n", target, original)
		fmt.Printf("Shim path:     %s\n\n", shimPath)
		if takeover {
			fmt.Printf("Takeover mode: original was backed up and replaced.\n\n")
		}
		fmt.Printf("Now when you run:\n  %s\n\n", target)
		fmt.Printf("It will start:\n  xit %s\n\n", target)
		fmt.Printf("To remove:\n  xit shim remove %s\n", target)
		return nil
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: xit shim remove <target>")
		}
		target := args[1]
		if !config.Exists(home) {
			return fmt.Errorf("Config not found. Run: xit init %s", target)
		}
		cfg, err := config.Load(home)
		if err != nil {
			return err
		}
		wasTakeover := cfg.Targets[target].Takeover
		if err := shim.Remove(home, cfg, target); err != nil {
			return err
		}
		fmt.Printf("XiT shim removed for %s.\n", target)
		if wasTakeover {
			shimPath, _ := os.UserHomeDir()
			shimPath = filepath.Join(shimPath, ".local", "bin", target)
			fmt.Printf("Original %s restored to %s\n", target, shimPath)
		}
		fmt.Printf("You can still run:\n  xit %s\n", target)
		return nil
	default:
		return fmt.Errorf("unknown shim command: %s", sub)
	}
}

func cmdWrapper(target string, args []string, globalMode string) int {
	// Handle special subcommands before wrapper logic.
	if target == "kimi" && len(args) > 0 && args[0] == "instructions" {
		cmdKimiInstructions()
		return 0
	}
	if target == "kimi" && len(args) > 0 && args[0] == "response-schema" {
		cmdKimiResponseSchema()
		return 0
	}
	if target == "kimi" && len(args) > 0 && args[0] == "status-bar-audit" {
		deep := false
		for _, a := range args[1:] {
			if a == "--deep" {
				deep = true
			}
		}
		if deep {
			cmdKimiStatusBarAuditDeep()
		} else {
			cmdKimiStatusBarAudit()
		}
		return 0
	}
	if target == "kimi" && len(args) > 0 && args[0] == "status-prototype" {
		return cmdKimiStatusPrototype(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "status-patch" {
		return cmdKimiStatusPatch(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "doctor" {
		home := userXiTHome()
		var cfg *config.Config
		if config.Exists(home) {
			cfg, _ = config.Load(home)
		}
		if cfg == nil {
			cfg = config.Default(version)
		}
		fmt.Print(cmdDoctorKimiDeep(home, cfg))
		return 0
	}
	if target == "kimi" && len(args) > 0 && args[0] == "benchmark" {
		return cmdKimiBenchmark(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "session" {
		return cmdKimiSession(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "rules" {
		if len(args) > 1 {
			return cmdKimiRulesSub(args[1:])
		}
		cmdKimiRules()
		return 0
	}
	if target == "kimi" && len(args) > 0 && args[0] == "hitrate" {
		return cmdKimiHitrate(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "impact" {
		return cmdKimiImpact(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "turn-status" {
		return cmdKimiTurnStatus(args[1:])
	}
	if target == "kimi" && len(args) > 0 && args[0] == "turn-diagnose" {
		return cmdKimiTurnDiagnose(args[1:])
	}

	if target == "claude" && len(args) > 0 && args[0] == "statusline" {
		return cmdClaudeStatusline(args[1:])
	}
	if target == "antigravity" && len(args) > 0 && args[0] == "statusline" {
		return cmdAntigravityStatusline(args[1:])
	}
	if target == "aider" && len(args) > 0 && args[0] == "rules" {
		if len(args) > 1 {
			return cmdAiderRulesSub(args[1:])
		}
		cmdAiderRules()
		return 0
	}
	if target == "aider" {
		fmt.Println("XiT Aider adapter is rules-only.")
		fmt.Println()
		fmt.Println("Install rules into current project:")
		fmt.Println("  xit aider rules install --scope project --yes")
		fmt.Println()
		fmt.Println("Preview rules:")
		fmt.Println("  xit aider rules preview")
		return 0
	}

	home := userXiTHome()
	if !config.Exists(home) {
		fmt.Fprintf(os.Stderr, "XiT is not initialized. Run: xit init %s\n", target)
		return 1
	}
	cfg, err := config.Load(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: cannot load config:", err)
		return 1
	}

	t, ok := cfg.Targets[target]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown target: %s\n", target)
		return 1
	}
	if !t.Enabled {
		fmt.Fprintf(os.Stderr, "%s is not initialized. Run: xit init %s\n", target, target)
		return 1
	}

	mode := globalMode
	if mode == "" {
		mode = cfg.DefaultMode
	}

	// Parse wrapper-level flags.
	unsafePty := false
	autoShims := true
	var wrapperArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--unsafe-pty" {
			unsafePty = true
			continue
		}
		if args[i] == "--no-auto-shims" {
			autoShims = false
			continue
		}
		wrapperArgs = append(wrapperArgs, args[i])
	}

	// Kimi TUI safety guard — run before path checks to prevent accidental broken TUI launch.
	if target == "kimi" && !unsafePty {
		fmt.Fprintf(os.Stderr, `
Kimi TUI compatibility warning.

XiT detected Kimi Code CLI, which uses a full-screen interactive TUI.
Current XiT session PTY may break Kimi input behavior:
  - input box may stop submitting
  - CPR/cursor-position warnings may appear
  - interactive session may become unusable

Recommended:
  Run Kimi directly:
    kimi

  Use XiT manually inside your workflow:
    xit --mode agent go test -v ./...
    xit --mode agent git diff

Advanced testing:
  xit kimi --unsafe-pty
  xit kimi --no-auto-shims

`)
		return 1
	}

	// Use original_path if available to avoid recursion through a shim.
	execPath := t.OriginalPath
	if execPath == "" {
		execPath = t.Path
	}
	if execPath == "" {
		fmt.Fprintf(os.Stderr, "%s path not configured. Run: xit init %s\n", target, target)
		return 1
	}
	if _, err := os.Stat(execPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s not found at configured path: %s\n", target, execPath)
		return 1
	}

	// Print wrapper notice.
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("╔══════════════════════════════════════════════════════════╗\n")
	b.WriteString(fmt.Sprintf("║  XiT %s Wrapper                                         \n", target))
	b.WriteString(fmt.Sprintf("║  target:  %s\n", target))
	b.WriteString(fmt.Sprintf("║  original: %s\n", execPath))
	b.WriteString(fmt.Sprintf("║  mode:    %s\n", mode))
	if autoShims {
		b.WriteString(fmt.Sprintf("║  auto shims: enabled\n"))
	} else {
		b.WriteString(fmt.Sprintf("║  auto shims: disabled\n"))
	}
	b.WriteString(fmt.Sprintf("║  tools:   %s\n", strings.Join(autoshim.DefaultTools, ", ")))
	b.WriteString("╚══════════════════════════════════════════════════════════╝\n")
	b.WriteString("\n")
	b.WriteString("note: XiT records this session and summarizes explicit xit-wrapped commands.\n")
	if autoShims {
		b.WriteString("      Auto shims are active: long command outputs inside this session\n")
		b.WriteString("      will be automatically compressed. Short and machine-readable\n")
		b.WriteString("      outputs will pass through.\n")
	} else {
		b.WriteString("      Auto shims are disabled. Only explicit xit-wrapped commands\n")
		b.WriteString("      will be compressed.\n")
	}
	b.WriteString("\n")
	b.WriteString("Important:\n")
	b.WriteString("For long terminal commands, ask the AI to use:\n")
	b.WriteString("  xit --mode agent <command>\n")
	b.WriteString("\n")
	fmt.Print(b.String())

	return session.RunSession(home, append([]string{execPath}, wrapperArgs...), mode, autoShims, false, os.Args[0])
}

func cmdSession(args []string, globalMode string) int {
	quiet := false
	mode := globalMode
	autoShims := true
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--quiet" {
			quiet = true
			continue
		}
		if args[i] == "--no-auto-shims" {
			autoShims = false
			continue
		}
		if args[i] == "--mode" && i+1 < len(args) {
			mode = args[i+1]
			i++
			continue
		}
		cmdArgs = append(cmdArgs, args[i])
	}

	if len(cmdArgs) < 1 {
		fmt.Fprintln(os.Stderr, "usage: xit session [--quiet] [--mode <mode>] [--no-auto-shims] <command...>")
		return 1
	}

	home := xitHome()
	return session.RunSession(home, cmdArgs, mode, autoShims, quiet, os.Args[0])
}

func cmdKimiHookTest(home string) error {
	scriptPath := filepath.Join(home, "hooks", "kimi-pretooluse-shell.sh")
	scriptExists := fileExists(scriptPath)
	scriptExec := false
	if scriptExists {
		if info, err := os.Stat(scriptPath); err == nil {
			scriptExec = info.Mode()&0111 != 0
		}
	}

	fmt.Println("XiT Kimi Hook Self-Test")
	fmt.Println()
	fmt.Printf("script exists:     %v\n", scriptExists)
	fmt.Printf("script executable: %v\n", scriptExec)

	if !scriptExists {
		fmt.Println()
		fmt.Println("result: hook script not found. Run: xit init kimi --method official_hook --yes")
		return nil
	}

	// Pipe sample payload to kimihook.RunHookCommand via stdin swap
	payload := `{"tool_name":"Shell","tool_input":{"command":"go test -v ./..."}}`
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Println("hook command:      pipe failed")
		return nil
	}
	oldStdin := os.Stdin
	os.Stdin = r
	go func() {
		w.WriteString(payload)
		w.Close()
	}()

	err = kimihook.RunHookCommand(home)
	os.Stdin = oldStdin
	if err != nil {
		fmt.Printf("hook command:      error (%v)\n", err)
		return nil
	}
	fmt.Println("hook command:      ok")

	logPath := filepath.Join(home, "kimi-hooks", "events.jsonl")
	if _, err := os.Stat(logPath); err == nil {
		fmt.Println("event log write:   ok")
		fmt.Println()
		fmt.Println("result: XiT hook command works locally")
	} else {
		fmt.Println("event log write:   no")
		fmt.Println()
		fmt.Println("result: hook ran but event log not found")
	}

	fmt.Println()
	fmt.Println("Important:")
	fmt.Println("This does NOT prove Kimi loads the hook.")
	fmt.Println("To verify Kimi runtime loading, open Kimi and run /hooks.")
	return nil
}

func cmdKimiInstructions() {
	fmt.Print(`XiT Kimi Hook Runtime Discovery Instructions

Phase A: Slash command discovery

1. Open real Kimi CLI from your project directory
2. Type the slash command:

   /hooks

3. Look for entries containing:
   - event: PreToolUse
   - matcher: Shell or Bash
   - command: ...kimi-pretooluse-shell.sh

If you see XiT hook entries, Kimi is loading the config.
If you do NOT see them, Kimi may not load project-scope config.

Phase B: User-scope fallback test

If /hooks does NOT show XiT hooks, try user-scope config:

   xit init kimi --method official_hook --scope user --yes

Then restart Kimi and run /hooks again.

Phase C: Shell command test

If /hooks DOES show XiT hooks, run test prompts inside Kimi:

   Please read-only execute:
   go test -v ./...
   grep -r "func" --include="*.go" .
   git status
   git diff

After exiting Kimi, check the event log:

   cat ~/.xit/kimi-hooks/events.jsonl

If the log is empty, the matcher or payload schema may be wrong.
Report your findings with:

   xit doctor kimi --deep
`)
}

func cmdKimiResponseSchema() {
	fmt.Print(`XiT Kimi Hook Response Schema Discovery

Source:      kimi-cli source code audit
File:        kimi_cli/hooks/runner.py
Method:      static analysis of installed Python package

Verified capabilities:

  observe hook:           verified
    - XiT hook receives PreToolUse JSON payload on stdin
    - XiT hook stdout is parsed for decision
    - XiT hook exits 0 with {} -> tool executes normally (passthrough)

  response passthrough:   verified
    - Exit 0 + {} or any non-JSON stdout -> action=allow
    - Tool executes with original arguments, hook stdout discarded

  block/deny:             supported
    - Exit code 2 -> action=block, reason=stderr.strip()
    - Exit 0 + JSON with hookSpecificOutput.permissionDecision="deny"
      -> action=block, reason=permissionDecisionReason
    - When blocked: tool is NOT executed, LLM sees ToolError with reason

  reason/message:         supported
    - Block reason is shown to LLM as error message
    - Empty reason falls back to "Blocked by PreToolUse hook"

  command rewrite:        unsupported
    - Kimi does NOT modify tool_input from hook response
    - tool.call(arguments) uses original arguments regardless of hook stdout

  output replacement:     unsupported
    - Hook stdout is only parsed for deny decision
    - When allowed, tool output is not replaced by hook stdout

Safe reroute status:
  - NOT YET IMPLEMENTED in XiT
  - Kimi CLI supports block with reason (similar to Claude deny)
  - XiT safe reroute would require: block + reason recommending xit auto <cmd>
  - Current XiT policy: observe-only, no deny, no reroute, no command rewrite

Next safe experiment:
  To verify block behavior without affecting real work:
  1. Create a temporary hook script that exits 2 with reason on stderr
  2. Or exits 0 with {"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"test block"}}
  3. Run a harmless command inside Kimi (e.g., echo hello)
  4. Verify Kimi shows the block reason instead of executing the command
  5. Revert the temporary script immediately after verification

`)
}

func cmdKimiStatusBarAuditDeep() {
	r := kimistatus.RunAudit()
	fmt.Print(kimistatus.FormatAuditReport(r))
	fmt.Print(`
TUI wrapper feasibility:
- current PTY wrapper breaks Kimi input: yes (verified in dogfood)
- requirements for safe TUI wrapper:
  - raw mode passthrough
  - CPR (cursor position request) forwarding
  - SIGWINCH forwarding
  - bracketed paste passthrough
  - ANSI-safe repaint without corrupting prompt_toolkit state
  - non-destructive bottom area injection
- risk: high
- reason: prompt_toolkit manages its own screen buffer and input state;
  injecting extra output without coordination corrupts the layout

ACP / IDE route:
- detected: yes
- evidence: kimi_cli/acp/server.py — full ACP (Agent Control Protocol) server
- ACP is used by VS Code and IDE extensions to control Kimi as an agent
- ACP protocol: JSON-RPC over stdio
- ACP provides: new_session, prompt, cancel, list_sessions, set_session_mode
- ACP does NOT provide: inject to terminal status bar (different render path)
- possible for VS Code/IDE status bar: yes (via ACP client in extension)
- terminal status bar relevance: low (ACP is for IDE embedding, not terminal UI)

Recommended product path:
1. Keep observe default
2. Keep blocking reroute notice as optional testing mode
3. Add rules/instructions mode to teach Kimi to use xit auto proactively
4. Research native UI/status extension before attempting persistent terminal status bar
5. Only attempt TUI-safe wrapper after dedicated prototype (separate from main XiT)
6. IDE/ACP route: viable for VS Code status bar, out of XiT terminal scope

`)
}

func cmdKimiStatusBarAudit() {
	fmt.Print(`XiT Kimi Same-Window Status Bar Audit

Current verified:
- Kimi hooks load from ~/.kimi/config.toml
- PreToolUse Shell/Bash hooks trigger
- observe mode logs events
- deny response can block high-noise commands

Current limitation:
- deny reason appears as Shell tool ERROR
- Kimi does not automatically rerun xit auto
- hook stdout is not a non-blocking UI channel
- persistent bottom status bar is not implemented

Possible paths:
1. Native Kimi UI/status extension: best if Kimi supports it
2. TUI-safe wrapper: possible but high risk; current PTY wrapper breaks Kimi input
3. Rules/instructions mode: teach Kimi to use xit auto for noisy commands
4. Blocking reroute notice: available now, but not a true status bar

Recommendation:
- Keep observe mode as default
- Use blocking reroute only for testing
- Research native Kimi UI/status API before building persistent status bar
`)
}

func cmdKimiStatusPrototype(args []string) int {
	audit := false
	title := false
	dryRunPatch := false
	for _, a := range args {
		switch a {
		case "--audit":
			audit = true
		case "--title":
			title = true
		case "--dry-run-patch":
			dryRunPatch = true
		}
	}

	if !audit && !title && !dryRunPatch {
		// Default: show prototype options menu
		fmt.Println("XiT Kimi Same-Window Status Bar Prototype (v0.2.20)")
		fmt.Println()
		fmt.Println("Kimi bottom toolbar audit result:")
		fmt.Println("  BLOCKED without upstream support or experimental monkey patch.")
		fmt.Println()
		fmt.Println("Available prototypes:")
		fmt.Println("  xit kimi status-prototype --audit")
		fmt.Println("    Run full feasibility audit (same as xit kimi status-bar-audit --deep)")
		fmt.Println()
		fmt.Println("  xit kimi status-prototype --title")
		fmt.Println("    Set terminal title to current XiT stats (OSC 0 sequence)")
		fmt.Println("    Low risk, works in most terminal emulators, NOT a bottom bar")
		fmt.Println()
		fmt.Println("  xit kimi status-prototype --dry-run-patch")
		fmt.Println("    Generate experimental Python monkey-patch script")
		fmt.Println("    HIGH RISK — may break Kimi TUI, overwritten on updates")
		fmt.Println("    This is a dry-run generator; XiT does NOT install it automatically.")
		fmt.Println()
		fmt.Println("Current recommended path:")
		fmt.Println("  1. rules-mode (already working): xit kimi rules install --scope user --yes")
		fmt.Println("  2. terminal title (low-risk fallback): xit kimi status-prototype --title")
		fmt.Println("  3. wait for Kimi native status extension API")
		return 0
	}

	if audit {
		r := kimistatus.RunAudit()
		fmt.Print(kimistatus.FormatAuditReport(r))
		return 0
	}

	if title {
		home := userXiTHome()
		observed := 0
		rerouted := 0
		// Try to load stats from events log if available.
		if stats, err := kimihook.Stats(home); err == nil {
			observed = stats.Observed
			rerouted = stats.Rerouted
		}
		statusText := kimistatus.StatusTextFromStats(observed, rerouted)
		titleStr := kimistatus.TitleFromStatus(observed, rerouted, 0)
		seq := kimistatus.SetTerminalTitle(titleStr)
		fmt.Print(seq)
		fmt.Printf("Terminal title set to: %s\n", titleStr)
		fmt.Printf("Status: %s\n", statusText)
		fmt.Println("Note: This updates the terminal window/tab title, NOT the Kimi bottom toolbar.")
		return 0
	}

	if dryRunPatch {
		home := userXiTHome()
		outPath := kimistatus.DefaultPatchPath(home)
		if err := kimistatus.WritePatchScript(home, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "xit: failed to write patch script: %v\n", err)
			return 1
		}
		fmt.Println("XiT Kimi Bottom Toolbar Patch Script — DRY RUN")
		fmt.Println()
		fmt.Printf("Generated: %s\n", outPath)
		fmt.Println()
		fmt.Println("WARNING: This is an EXPERIMENTAL monkey-patch generator.")
		fmt.Println("XiT does NOT install it automatically.")
		fmt.Println()
		fmt.Println("To apply (AT YOUR OWN RISK):")
		fmt.Println("  1. Locate your Kimi Python environment site-packages directory:")
		fmt.Printf("     %s\n", kimistatus.KimiPackagePath())
		fmt.Println("  2. Copy the generated script to that directory as sitecustomize.py")
		fmt.Println("  3. Restart Kimi")
		fmt.Println("  4. If Kimi crashes or behaves strangely, REMOVE sitecustomize.py immediately")
		fmt.Println()
		fmt.Println("Risks:")
		fmt.Println("  - Kimi updates will overwrite or conflict with the patch")
		fmt.Println("  - Incorrect patch can corrupt prompt_toolkit state")
		fmt.Println("  - No guarantee of stability across Kimi versions")
		return 0
	}

	return 0
}

func cmdKimiStatusPatch(args []string) int {
	if len(args) < 1 {
		fmt.Println("usage: xit kimi status-patch <status|preview|dry-run|validate|check-update|install|uninstall>")
		fmt.Println()
		fmt.Println("  status       Check if Kimi can be patched (read-only)")
		fmt.Println("  preview      Show current toolbar preview and rotation candidates")
		fmt.Println("  dry-run      Show patch plan without modifying files")
		fmt.Println("  validate     Apply patch to temp copy and run py_compile (no real changes)")
		fmt.Println("  check-update Read-only check if patch is still valid after Kimi update")
		fmt.Println("  install      Apply patch to Kimi prompt.py (requires --yes --accept-risk)")
		fmt.Println("  uninstall    Restore original prompt.py from XiT backup")
		return 1
	}

	sub := args[0]
	hasYes := false
	hasAcceptRisk := false
	hasForce := false
	for _, a := range args[1:] {
		switch a {
		case "--yes":
			hasYes = true
		case "--accept-risk":
			hasAcceptRisk = true
		case "--force":
			hasForce = true
		}
	}

	pkgDir, err := kimistatus.LocateKimiPackage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "xit: cannot locate Kimi package: %v\n", err)
		return 1
	}
	res := kimistatus.CheckPatchable(pkgDir)
	home := userXiTHome()

	switch sub {
	case "status":
		fmt.Println("XiT Kimi Status Patch")
		fmt.Println()
		fmt.Printf("kimi:       %s\n", orUnknown(res.KimiPath))
		fmt.Printf("version:    %s\n", orUnknown(res.KimiVersion))
		fmt.Printf("package:    %s\n", res.PackageDir)
		fmt.Printf("target:     %s\n", res.PromptPyPath)
		fmt.Printf("patchable:  %v\n", res.Patchable)
		fmt.Printf("installed:  %v\n", res.Installed)
		backup := kimistatus.FindBackup(res.PromptPyPath)
		if backup != "" {
			fmt.Printf("backup:     %s\n", backup)
		} else {
			fmt.Println("backup:     none")
		}
		fmt.Println("risk:       high")
		fmt.Println("note:       modifies local Kimi package; uninstall restores backup")
		fmt.Println("patch_type: monkey_patch")
		fmt.Println("official_api: no")
		fmt.Println("kimi_update_sensitive: yes")
		if res.Installed {
			if backup != "" {
				fmt.Println("rollback_ready: yes")
			} else {
				fmt.Println("rollback_ready: no")
				fmt.Println("warning:    backup missing")
			}
		}
		fmt.Println("rollback:   xit kimi status-patch uninstall --yes")
		preview := kimistatus.ComputeToolbarPreview(home)
		fmt.Printf("toolbar_preview: %s\n", preview.Preview)
		fmt.Printf("toolbar_position: %s\n", preview.Position)
		fmt.Printf("toolbar_mode: %s\n", preview.Mode)
		fmt.Printf("toolbar_scope: %s\n", preview.ToolbarScope)
		fmt.Printf("turn_lifecycle: %s\n", "expected")
		fmt.Printf("turn_state_file: %s\n", ".xit/state/turn.json")
		fmt.Printf("rotation: %s\n", preview.RotationScope)
		fmt.Printf("rotation_interval: %s\n", preview.RotationInterval)
		fmt.Printf("session_aggregate_in_toolbar: %s\n", "no")
		fmt.Printf("session_source: %s\n", "kimi_process_start")
		fmt.Printf("language: %s\n", preview.Language)
		fmt.Printf("style: %s\n", preview.Style)
		fmt.Printf("history_in_ready: %s\n", "no")
		fmt.Printf("raw_log_in_ready: %s\n", "no")
		fmt.Printf("english_in_toolbar: %s\n", "no")
		fmt.Printf("real_prompt_audited: %s\n", "yes")
		fmt.Printf("visible_target: %s\n", "context_line_left")
		if res.Reason != "" {
			fmt.Printf("reason:     %s\n", res.Reason)
		}
		return 0

	case "check-update":
		uc := kimistatus.CheckUpdate(pkgDir)
		fmt.Println("XiT Kimi Status Patch — Update Check")
		fmt.Println()
		fmt.Printf("version:         %s\n", orUnknown(uc.Version))
		fmt.Printf("prompt_hash:     %s\n", uc.PromptHash)
		fmt.Printf("patch_marker:    %s\n", uc.PatchMarker)
		if uc.BackupExists {
			fmt.Println("backup:          present")
		} else {
			fmt.Println("backup:          absent")
		}
		if uc.PlacementValid {
			fmt.Println("placement:       ok")
		} else {
			fmt.Println("placement:       invalid")
		}
		fmt.Printf("action:          %s\n", uc.Action)
		return 0

	case "preview":
		preview := kimistatus.ComputeToolbarPreview(home)
		fmt.Println("XiT Kimi Toolbar Preview")
		fmt.Println()
		fmt.Println("toolbar_preview:")
		fmt.Printf("ready: %s\n", preview.ReadyText)
		fmt.Printf("guarding: %s\n", preview.GuardingText)
		fmt.Printf("absorbing: %s\n", preview.AbsorbingText)
		fmt.Printf("absorbing_progress: %s\n", preview.AbsorbingProgressText)
		fmt.Printf("completed: %s\n", preview.CompletedText)
		fmt.Println("turn_result_with_auto:")
		for _, line := range preview.TurnResultWithAuto {
			fmt.Printf("- %s\n", line)
		}
		fmt.Println("turn_result_without_auto:")
		for _, line := range preview.TurnResultWithoutAuto {
			fmt.Printf("- %s\n", line)
		}
		fmt.Println()
		fmt.Printf("position: %s\n", preview.Position)
		fmt.Printf("mode: %s\n", preview.Mode)
		fmt.Printf("toolbar_scope: %s\n", preview.ToolbarScope)
		fmt.Printf("rotation: %s\n", preview.RotationScope)
		fmt.Printf("rotation_interval: %s\n", preview.RotationInterval)
		fmt.Printf("unit: %s\n", preview.Unit)
		fmt.Printf("token_method: %s\n", preview.TokenMethod)
		fmt.Printf("toolbar_example: %s\n", preview.ToolbarExample)
		fmt.Printf("history_in_ready: %s\n", "no")
		fmt.Printf("raw_log_in_ready: %s\n", "no")
		fmt.Printf("english_in_toolbar: %s\n", "no")
		return 0

	case "dry-run":
		if res.PromptPyPath == "" {
			fmt.Fprintln(os.Stderr, "xit: cannot find prompt.py")
			return 1
		}
		if _, err := os.Stat(res.PromptPyPath); err != nil {
			fmt.Fprintf(os.Stderr, "xit: prompt.py not readable: %v\n", err)
			return 1
		}
		diff, err := kimistatus.DryRunPatch(res.PromptPyPath, home)
		if err != nil {
			fmt.Fprintf(os.Stderr, "xit: dry-run failed: %v\n", err)
			return 1
		}
		fmt.Println("XiT Kimi Status Patch — DRY RUN")
		fmt.Println()
		fmt.Printf("Target:   %s\n", res.PromptPyPath)
		fmt.Printf("Version:  %s\n", orUnknown(res.KimiVersion))
		fmt.Println()
		fmt.Println("Lines that would be added:")
		fmt.Println(diff)
		fmt.Println()
		fmt.Println("No files were modified.")
		fmt.Println("Patch syntax validation: not run in dry-run")
		fmt.Println("To validate on temp copy:")
		fmt.Println("  xit kimi status-patch validate")
		return 0

	case "validate":
		if res.PromptPyPath == "" {
			fmt.Fprintln(os.Stderr, "xit: cannot find prompt.py")
			return 1
		}
		if _, err := os.Stat(res.PromptPyPath); err != nil {
			fmt.Fprintf(os.Stderr, "xit: prompt.py not readable: %v\n", err)
			return 1
		}
		if err := kimistatus.ValidatePatch(res.PromptPyPath, home); err != nil {
			fmt.Fprintf(os.Stderr, "xit: validate failed: %v\n", err)
			return 1
		}
		fmt.Println("XiT Kimi Status Patch — VALIDATE")
		fmt.Println()
		fmt.Printf("Target:              %s\n", res.PromptPyPath)
		fmt.Println("temp patch:          ok")
		fmt.Println("py_compile:          ok")
		fmt.Println("real Kimi package modified: no")
		return 0

	case "install":
		if !hasYes || !hasAcceptRisk {
			fmt.Fprintln(os.Stderr, "error: install requires both --yes and --accept-risk")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "WARNING: This patch modifies your local Kimi installation.")
			fmt.Fprintln(os.Stderr, "It may break on Kimi updates and corrupt the TUI.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "To proceed, run:")
			fmt.Fprintln(os.Stderr, "  xit kimi status-patch install --yes --accept-risk")
			return 1
		}
		if res.Installed {
			fmt.Fprintln(os.Stderr, "error: XiT patch already installed")
			fmt.Fprintln(os.Stderr, "Run uninstall first:")
			fmt.Fprintln(os.Stderr, "  xit kimi status-patch uninstall --yes")
			return 1
		}
		if !res.Patchable && !hasForce {
			fmt.Fprintf(os.Stderr, "error: %s\n", res.Reason)
			fmt.Fprintln(os.Stderr, "Use --force to override version check.")
			return 1
		}
		if err := kimistatus.InstallPatch(res.PromptPyPath, home, hasForce); err != nil {
			fmt.Fprintf(os.Stderr, "xit: install failed: %v\n", err)
			return 1
		}
		fmt.Println("XiT Kimi Status Patch installed.")
		fmt.Println()
		fmt.Printf("Target:   %s\n", res.PromptPyPath)
		fmt.Printf("Backup:   %s\n", kimistatus.FindBackup(res.PromptPyPath))
		fmt.Println()
		fmt.Println("Restart Kimi to see the status bar update.")
		fmt.Println("If Kimi crashes or behaves strangely, run:")
		fmt.Println("  xit kimi status-patch uninstall --yes")
		return 0

	case "uninstall":
		if !hasYes {
			fmt.Fprintln(os.Stderr, "error: uninstall requires --yes")
			return 1
		}
		if err := kimistatus.UninstallPatch(res.PromptPyPath); err != nil {
			fmt.Fprintf(os.Stderr, "xit: uninstall failed: %v\n", err)
			return 1
		}
		fmt.Println("XiT Kimi Status Patch uninstalled.")
		fmt.Printf("Restored: %s\n", res.PromptPyPath)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown status-patch command: %s\n", sub)
		return 1
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func cmdKimiRules() {
	fmt.Println("XiT Kimi Rules Mode (v0.2.34)")
	fmt.Println()
	fmt.Println("Rules mode teaches Kimi to proactively use `xit auto` for high-output commands,")
	fmt.Println("bypassing the need for deny/reroute hooks entirely.")
	fmt.Println()
	fmt.Println("How it works:")
	fmt.Println("  XiT installs a skill file at ~/.kimi/skills/xit/SKILL.md")
	fmt.Println("  Kimi auto-discovers and injects skills into its system prompt at startup.")
	fmt.Println("  The skill file tells Kimi to route high-noise commands through xit auto.")
	fmt.Println()
	fmt.Println("Install (user scope, persists across all projects):")
	fmt.Println("  xit kimi rules install --scope user --yes")
	fmt.Println()
	fmt.Println("Install (project scope, applies to current directory only):")
	fmt.Println("  xit kimi rules install --scope project --yes")
	fmt.Println()
	fmt.Println("Check status:")
	fmt.Println("  xit kimi rules status --scope user")
	fmt.Println("  xit kimi rules status --scope project")
	fmt.Println()
	fmt.Println("Remove:")
	fmt.Println("  xit kimi rules uninstall --scope user --yes")
	fmt.Println()
	fmt.Println("Test the rules with a copy-paste prompt:")
	fmt.Println("  xit kimi rules dogfood")
	fmt.Println()
	fmt.Println("Skill file content preview:")
	fmt.Println("  Use `xit auto` for: go test -v ./..., git diff, grep -r, docker logs,")
	fmt.Println("  npm test, cargo test, pytest, find (large trees).")
	fmt.Println("  Do NOT use for: git status, git branch, npm install, --json/--porcelain flags,")
	fmt.Println("  short diagnostics, jq pipelines.")
}

// cmdKimiRulesSub handles `xit kimi rules <install|status|uninstall|dogfood> [flags]`
func cmdKimiRulesSub(args []string) int {
	if len(args) < 1 {
		cmdKimiRules()
		return 0
	}
	sub := args[0]
	scope, _ := extractScopeFlag(args[1:])
	if scope == "project" && sub != "dogfood" {
		// default scope for rules is user, not project
		// only override if explicitly passed
		hasExplicit := false
		for _, a := range args[1:] {
			if a == "--scope" || strings.HasPrefix(a, "--scope=") {
				hasExplicit = true
				break
			}
		}
		if !hasExplicit {
			scope = "user"
		}
	}

	switch sub {
	case "install":
		if !hasYesFlag(args[1:]) {
			fmt.Fprintln(os.Stderr, "xit kimi rules install requires --yes to confirm")
			return 1
		}
		path, err := kimirulesinstall.Install(scope)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Printf("XiT Kimi rules installed (%s scope).\n", scope)
		fmt.Printf("skill: %s\n", path)
		fmt.Println()
		fmt.Println("Restart Kimi for the skill to take effect.")
		fmt.Println("Verify: xit kimi rules status --scope", scope)
		return 0

	case "status":
		st, err := kimirulesinstall.Status(scope)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println("XiT Kimi Rules Status")
		fmt.Println()
		fmt.Printf("scope:     %s\n", st.Scope)
		fmt.Printf("skill:     %s\n", st.SkillPath)
		if st.Installed {
			fmt.Printf("installed: yes\n")
			fmt.Printf("active:    yes (Kimi injects skill at startup)\n")
		} else {
			fmt.Printf("installed: no\n")
			fmt.Printf("active:    no\n")
			fmt.Printf("\nTo install: xit kimi rules install --scope %s --yes\n", scope)
		}
		return 0

	case "uninstall":
		if !hasYesFlag(args[1:]) {
			fmt.Fprintln(os.Stderr, "xit kimi rules uninstall requires --yes to confirm")
			return 1
		}
		path, err := kimirulesinstall.Uninstall(scope)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Printf("XiT Kimi rules uninstalled (%s scope).\n", scope)
		fmt.Printf("removed: %s\n", path)
		return 0

	case "preview":
		fmt.Println("Skill file content preview:")
		fmt.Println()
		fmt.Println(kimirulesinstall.SkillFileContent())
		return 0

	case "dogfood":
		cmdKimiRulesDogfood()
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: xit kimi rules %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: xit kimi rules [install|status|uninstall|preview|dogfood] [--scope user|project] [--yes]")
		return 1
	}
}

func cmdKimiRulesDogfood() {
	fmt.Println("XiT Rules Mode Dogfood Prompt")
	fmt.Println()
	fmt.Println("Copy this prompt into Kimi to verify the rules skill is active:")
	fmt.Println()
	fmt.Println("---")
	fmt.Println("I want to run our Go test suite and inspect the diff since the last tag.")
	fmt.Println("Please run: go test -v ./... and then git diff $(git describe --tags --abbrev=0)..HEAD")
	fmt.Println("---")
	fmt.Println()
	fmt.Println("Expected behavior if rules are active:")
	fmt.Println("  Kimi should use: xit auto go test -v ./...")
	fmt.Println("  Kimi should use: xit auto git diff $(git describe --tags --abbrev=0)..HEAD")
	fmt.Println()
	fmt.Println("If Kimi runs the bare commands instead, the skill is not loaded.")
	fmt.Println("Check: xit kimi rules status --scope user")
	fmt.Println("Then:  restart Kimi (skill injection happens at startup).")
}

func cmdAiderRules() {
	fmt.Println("XiT Aider Rules Mode")
	fmt.Println()
	fmt.Println("Aider adapter is rules-only. XiT installs a rules file and configures")
	fmt.Println("Aider to read it via .aider.conf.yml.")
	fmt.Println()
	fmt.Println("Install (project scope only):")
	fmt.Println("  xit aider rules install --scope project --yes")
	fmt.Println()
	fmt.Println("Check status:")
	fmt.Println("  xit aider rules status --scope project")
	fmt.Println()
	fmt.Println("Remove:")
	fmt.Println("  xit aider rules uninstall --scope project --yes")
	fmt.Println()
	fmt.Println("Preview rules content:")
	fmt.Println("  xit aider rules preview")
	fmt.Println()
	fmt.Println("Note: Aider does not support hooks or command-backed statusLine.")
	fmt.Println("      XiT Aider integration is rules-only.")
}

func cmdAiderRulesSub(args []string) int {
	if len(args) < 1 {
		cmdAiderRules()
		return 0
	}
	sub := args[0]
	scope, _ := extractScopeFlag(args[1:])
	if scope != "project" {
		fmt.Fprintln(os.Stderr, "only project scope is supported for Aider rules in this version")
		return 1
	}

	switch sub {
	case "install":
		if !hasYesFlag(args[1:]) {
			fmt.Fprintln(os.Stderr, "xit aider rules install requires --yes to confirm")
			return 1
		}
		if err := aiderrulesinstall.InstallProject(""); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println("XiT Aider rules installed (project scope).")
		fmt.Printf("rules: %s\n", aiderrulesinstall.RulesPath(""))
		fmt.Printf("config: %s\n", aiderrulesinstall.ConfigPath(""))
		fmt.Println()
		fmt.Println("Run aider in this project to load the rules.")
		return 0

	case "status":
		st, err := aiderrulesinstall.StatusProject("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println("XiT Aider Rules Status")
		fmt.Println()
		fmt.Printf("scope:            %s\n", st.Scope)
		fmt.Printf("rules_path:       %s\n", st.RulesPath)
		fmt.Printf("rules_exists:     %v\n", st.RulesExists)
		fmt.Printf("config_path:      %s\n", st.ConfigPath)
		fmt.Printf("config_exists:    %v\n", st.ConfigExists)
		fmt.Printf("installed:        %v\n", st.Installed)
		fmt.Printf("read_configured:  %v\n", st.ReadConfigured)
		return 0

	case "uninstall":
		if !hasYesFlag(args[1:]) {
			fmt.Fprintln(os.Stderr, "xit aider rules uninstall requires --yes to confirm")
			return 1
		}
		if err := aiderrulesinstall.UninstallProject(""); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println("XiT Aider rules uninstalled (project scope).")
		return 0

	case "preview":
		fmt.Println("Aider rules content preview:")
		fmt.Println()
		fmt.Println(aiderrulesinstall.Preview())
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: xit aider rules %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: xit aider rules [install|status|uninstall|preview] [--scope project] [--yes]")
		return 1
	}
}

func cmdKimiHitrate(args []string) int {
	window := 2 * time.Hour
	useJSON := false
	verbose := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			useJSON = true
		case a == "--verbose":
			verbose = true
		case strings.HasPrefix(a, "--last"):
			var val string
			if strings.Contains(a, "=") {
				val = strings.SplitN(a, "=", 2)[1]
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
			if d, err := time.ParseDuration(val); err == nil {
				window = d
			}
		}
	}

	projectHome := xitHome()
	// Hook events are always stored in the real user home (~/.xit), not XIT_HOME.
	actualUserHomeDir := os.Getenv("HOME")
	if actualUserHomeDir == "" {
		var err error
		actualUserHomeDir, err = os.UserHomeDir()
		if err != nil {
			actualUserHomeDir = "."
		}
	}
	userHome := filepath.Join(actualUserHomeDir, ".xit")

	report, err := hitrate.ComputeReport(userHome, projectHome, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if useJSON {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	fmt.Print(hitrate.FormatReport(report, verbose))
	return 0
}

func cmdKimiImpact(args []string) int {
	window := 2 * time.Hour
	useJSON := false
	kimiContextTokens := 0

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			useJSON = true
		case strings.HasPrefix(a, "--kimi-context"):
			var val string
			if strings.Contains(a, "=") {
				val = strings.SplitN(a, "=", 2)[1]
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
			kimiContextTokens = impact.ParseContextTokens(val)
		case strings.HasPrefix(a, "--last"):
			var val string
			if strings.Contains(a, "=") {
				val = strings.SplitN(a, "=", 2)[1]
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
			if d, err := time.ParseDuration(val); err == nil {
				window = d
			}
		}
	}

	projectHome := xitHome()
	report, err := impact.ComputeReport(projectHome, window, kimiContextTokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if useJSON {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	fmt.Print(impact.FormatReport(report))
	return 0
}

func cmdKimiSession(args []string) int {
	useJSON := false
	for _, a := range args {
		if a == "--json" {
			useJSON = true
		}
	}

	home := xitHome()
	m, err := history.ComputeSessionMetrics(home, 2*time.Hour)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if useJSON {
		data, _ := json.MarshalIndent(m, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	fmt.Print(history.FormatSessionMetrics(m, true))
	return 0
}

func cmdKimiBenchmark(args []string) int {
	hasYes := false
	hasRun := false
	for _, a := range args {
		if a == "--yes" {
			hasYes = true
		}
		if a == "--run" {
			hasRun = true
		}
	}

	home, source := resolveHistoryHome()
	g, _ := history.ComputeGain(home)

	fmt.Println("XiT Kimi Benchmark")
	fmt.Println()
	fmt.Printf("History source: %s\n", source)
	fmt.Println()
	fmt.Println("Current dogfood:")
	fmt.Printf("  total commands:     %d\n", g.TotalCommands)
	fmt.Printf("  overall reduction:  %.1f%%\n", g.EstimatedReduction*100)
	fmt.Println()

	if len(g.TopCommands) > 0 {
		fmt.Println("Top commands:")
		for _, tc := range g.TopCommands {
			fmt.Printf("  %s: saved %d bytes\n", tc.Command, tc.Saved)
		}
		fmt.Println()
	}

	fmt.Println("Representative compression range from dogfood:")
	if g.TotalCommands > 0 {
		fmt.Printf("  go test -v:          typically 95-99%%\n")
		fmt.Printf("  grep -rn func:       typically 60-70%%\n")
		fmt.Printf("  find go files:       typically 20-30%%\n")
		fmt.Printf("  overall average:     %.1f%%\n", g.EstimatedReduction*100)
	} else {
		fmt.Println("  No history yet. Run some commands through xit auto to build stats.")
	}
	fmt.Println()

	if g.EstimatedReduction >= 0.6 {
		fmt.Println("Verdict:")
		fmt.Println("  XiT reaches 60-90% compression range in Kimi dogfood: yes")
	} else if g.TotalCommands > 0 {
		fmt.Println("Verdict:")
		fmt.Println("  XiT reaches 60-90% compression range in Kimi dogfood: partial")
	} else {
		fmt.Println("Verdict:")
		fmt.Println("  XiT reaches 60-90% compression range in Kimi dogfood: unknown (no data)")
	}

	if hasRun && !hasYes {
		fmt.Fprintln(os.Stderr, "error: --run requires --yes")
		return 1
	}

	if hasRun && hasYes {
		fmt.Println()
		fmt.Println("Running representative commands...")
		// These are read-only safe commands
		_ = cmdAuto([]string{"go", "test", "-v", "./..."})
		_ = cmdAuto([]string{"grep", "-rn", "^func ", "."})
		_ = cmdAuto([]string{"find", ".", "-name", "*.go", "-type", "f"})
	}

	return 0
}

func resolveHistoryHome() (string, string) {
	projectHome := xitHome()
	if _, err := os.Stat(filepath.Join(projectHome, "history.jsonl")); err == nil {
		return projectHome, filepath.Join(projectHome, "history.jsonl")
	}
	globalHome := userXiTHome()
	return globalHome, filepath.Join(globalHome, "history.jsonl")
}

func cmdBenchCompression(args []string) int {
	hasYes := false
	hasRun := false
	for _, a := range args {
		if a == "--yes" {
			hasYes = true
		}
		if a == "--run" {
			hasRun = true
		}
	}

	if hasRun && !hasYes {
		fmt.Fprintln(os.Stderr, "error: --run requires --yes")
		return 1
	}

	home, source := resolveHistoryHome()
	br, _ := history.ComputeBenchReport(home)
	g, _ := history.ComputeGain(home)

	fmt.Println("XiT Compression Benchmark")
	fmt.Println()
	fmt.Printf("History source: %s\n", source)
	fmt.Println()
	fmt.Println("History summary:")
	fmt.Printf("  commands:     %d\n", br.TotalCommands)
	fmt.Printf("  raw bytes:    %d\n", br.TotalRawBytes)
	fmt.Printf("  summary bytes:%d\n", br.TotalSummaryBytes)
	fmt.Printf("  reduction:    %.1f%%\n", br.OverallReduction*100)
	fmt.Println()

	if len(br.ByFilter) > 0 {
		fmt.Println("Quality by filter:")
		var names []string
		for n := range br.ByFilter {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			bg := br.ByFilter[n]
			fmt.Printf("  %-10s: %.1f%% avg reduction (%d runs)\n", n, bg.AvgReduction*100, bg.Count)
		}
		fmt.Println()
	}

	if len(br.ByConfidence) > 0 {
		fmt.Println("Quality by confidence:")
		for _, lvl := range []string{"high", "medium", "low"} {
			bg, ok := br.ByConfidence[lvl]
			if !ok {
				continue
			}
			fmt.Printf("  %-7s: %.1f%% avg reduction (%d runs)\n", lvl, bg.AvgReduction*100, bg.Count)
		}
		fmt.Println()
	}

	fmt.Println("Policy hardening:")
	for _, p := range []string{"should_compress", "should_passthrough", "needs_review"} {
		bg, ok := br.ByPolicy[p]
		if !ok {
			continue
		}
		fmt.Printf("  %-18s: %d runs, %.1f%% avg reduction\n", p, bg.Count, bg.AvgReduction*100)
	}
	fmt.Println()

	if br.TotalCommands == 0 {
		fmt.Println("Verdict:")
		fmt.Println("  No history yet. Run some commands through xit auto to build stats.")
	} else if br.OverallReduction >= 0.5 {
		fmt.Println("Verdict:")
		fmt.Println("  XiT compression quality: excellent (≥50% overall reduction)")
	} else if br.OverallReduction >= 0.3 {
		fmt.Println("Verdict:")
		fmt.Println("  XiT compression quality: good (≥30% overall reduction)")
	} else {
		fmt.Println("Verdict:")
		fmt.Println("  XiT compression quality: partial (<30% overall reduction)")
	}

	if hasRun && hasYes {
		fmt.Println()
		fmt.Println("Running representative read-only commands...")
		_ = cmdAuto([]string{"go", "test", "-v", "./..."})
		_ = cmdAuto([]string{"grep", "-rn", "^func ", "."})
		_ = cmdAuto([]string{"find", ".", "-name", "*.go", "-type", "f"})
	}

	_ = g
	return 0
}

// ---------- Claude StatusLine ----------

const (
	statusLineGoldColor = "\033[38;5;178m"
	statusLineReset     = "\033[0m"
	statusLineFallback  = "吸T神功 · Claude · 准备就绪"
	statusLineReady     = "吸T神功 · Claude · 准备就绪"
)

func cmdClaudeStatusline(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return cmdClaudeStatuslineInstall(args[1:])
		case "status":
			return cmdClaudeStatuslineStatus()
		case "uninstall":
			return cmdClaudeStatuslineUninstall(args[1:])
		}
	}

	useJSON := false
	for _, a := range args {
		if a == "--json" {
			useJSON = true
		}
	}

	line, data := computeClaudeStatuslineText()

	if useJSON {
		out, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(out))
		return 0
	}

	noColor := os.Getenv("NO_COLOR") != ""
	if noColor {
		fmt.Println(line)
	} else {
		fmt.Printf("%s%s%s\n", statusLineGoldColor, line, statusLineReset)
	}
	return 0
}

func computeClaudeStatuslineText() (string, map[string]interface{}) {
	// fail-open: any panic returns fallback
	defer func() { recover() }() //nolint

	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	if homeDir == "" {
		return statusLineFallback, map[string]interface{}{"line": statusLineFallback, "source": "no_home"}
	}
	userXiT := filepath.Join(homeDir, ".xit")
	projectHome := xitHome()
	window := 10 * time.Minute
	now := time.Now()

	// 1. Check autostate for running or recently completed xit auto.
	autoState, autoPath, _ := autostate.Read(projectHome, userXiT)

	// Check hook installed (project scope only, observe mode).
	hookInstalled := false
	if st, err := claudehook.Status(claudehook.ProjectSettingsPath(), userXiT); err == nil {
		hookInstalled = st.Installed
	}

	var line string
	source := "history"

	switch {
	case autostate.IsRunningFresh(autoState, now):
		line = "吸T神功 · 正在吸T中"
		source = "autostate_running"
	case autostate.IsCompletedFresh(autoState, now) && autoState != nil && autoState.SavedBytes > 0:
		line = fmt.Sprintf("吸T神功 · 本次省%s Token", formatTokenCount(int(autoState.SavedBytes/4)))
		source = "autostate_completed"
	default:
		// Recent hitrate (10 min window).
		report, _ := hitrate.ComputeReportForAdapter("claude", userXiT, projectHome, window)
		hasRecentEvents := report != nil && report.ShellCommandsSeen > 0
		hitRatePct := 0.0
		verdictPass := false
		if report != nil && (report.ShouldCompress.Total+report.ShouldPassthrough.Total) > 0 {
			total := report.ShouldCompress.Total + report.ShouldPassthrough.Total
			correct := report.ShouldCompress.CorrectlyWrapped + report.ShouldPassthrough.CorrectlyPassthrough
			hitRatePct = float64(correct) / float64(total) * 100
			verdictPass = report.Verdict == "pass"
		}

		// Recent token savings from xit auto history (10 min).
		savedTokens := 0
		if m, err := history.ComputeSessionMetrics(projectHome, window); err == nil && m != nil {
			if m.CurrentSession.SavedBytes > 0 {
				savedTokens = m.CurrentSession.SavedBytes / 4
			}
		}
		if savedTokens == 0 {
			if m, err := history.ComputeSessionMetrics(userXiT, window); err == nil && m != nil {
				if m.CurrentSession.SavedBytes > 0 {
					savedTokens = m.CurrentSession.SavedBytes / 4
				}
			}
		}

		// Build one-line text by priority.
		switch {
		case hasRecentEvents && verdictPass && savedTokens > 0:
			line = fmt.Sprintf("吸T神功 · 本次省%s · 命中率%.0f%%", formatTokenCount(savedTokens), hitRatePct)
		case hasRecentEvents && verdictPass:
			line = fmt.Sprintf("吸T神功 · Claude · 命中率%.0f%%", hitRatePct)
		case savedTokens > 0 && hasRecentEvents:
			line = fmt.Sprintf("吸T神功 · 本次省%s · 命中率%.0f%%", formatTokenCount(savedTokens), hitRatePct)
		case savedTokens > 0:
			line = fmt.Sprintf("吸T神功 · 本次省%s Token", formatTokenCount(savedTokens))
		case hookInstalled:
			line = statusLineReady
		default:
			line = statusLineFallback
		}

		verdictStr := ""
		if report != nil {
			verdictStr = report.Verdict
		}
		data := map[string]interface{}{
			"line":                line,
			"color":               "gold",
			"hit_rate":            hitRatePct,
			"saved_tokens_recent": savedTokens,
			"hook_installed":      hookInstalled,
			"has_recent_events":   hasRecentEvents,
			"verdict":             verdictStr,
			"source":              source,
			"autostate_path":      autoPath,
		}
		return line, data
	}

	data := map[string]interface{}{
		"line":           line,
		"color":          "gold",
		"source":         source,
		"hook_installed": hookInstalled,
		"autostate_path": autoPath,
	}
	return line, data
}

func formatTokenCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// claudeLocalSettingsPath returns the project-local Claude settings path.
func claudeLocalSettingsPath() string {
	return filepath.Join(".claude", "settings.local.json")
}

// readRawSettings reads a Claude settings file as a generic map to preserve all fields.
func readRawSettings(path string) (map[string]json.RawMessage, error) {
	m := make(map[string]json.RawMessage)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", path, err)
	}
	return m, nil
}

// writeRawSettings writes a generic map back to a Claude settings file.
func writeRawSettings(path string, m map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func cmdClaudeStatuslineInstall(args []string) int {
	scope := "project-local"
	yes := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--yes":
			yes = true
		case args[i] == "--scope" && i+1 < len(args):
			scope = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--scope="):
			scope = strings.TrimPrefix(args[i], "--scope=")
		}
	}

	if scope != "project-local" {
		fmt.Fprintf(os.Stderr, "xit claude statusline install: only --scope project-local is supported\n")
		return 1
	}

	settingsPath := claudeLocalSettingsPath()
	m, err := readRawSettings(settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", settingsPath, err)
		return 1
	}

	// Check if already installed.
	if _, exists := m["statusLine"]; exists && !yes {
		fmt.Printf("XiT Claude StatusLine already present in %s\n", settingsPath)
		fmt.Println("Use --yes to overwrite.")
		return 1
	}

	// Backup if file exists.
	if _, statErr := os.Stat(settingsPath); statErr == nil {
		if backupPath, backupErr := claudehook.BackupSettings(settingsPath); backupErr == nil && backupPath != "" {
			fmt.Printf("backup: %s\n", backupPath)
		}
	}

	// Resolve absolute path to xit for reliable execution inside Claude Code.
	xitPath, lookErr := exec.LookPath("xit")
	if lookErr != nil || xitPath == "" {
		// Fallback to common install locations
		candidates := []string{
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "xit"),
			"/usr/local/bin/xit",
			"/opt/homebrew/bin/xit",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				xitPath = c
				break
			}
		}
	}
	if xitPath == "" {
		fmt.Fprintln(os.Stderr, "cannot find xit in PATH; ensure xit is installed and in PATH before installing statusLine")
		return 1
	}

	// Build statusLine value.
	statusLineVal := map[string]interface{}{
		"type":    "command",
		"command": xitPath + " claude statusline",
		"padding": 0,
	}
	slData, _ := json.Marshal(statusLineVal)
	m["statusLine"] = json.RawMessage(slData)

	if err := writeRawSettings(settingsPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", settingsPath, err)
		return 1
	}

	fmt.Println("XiT Claude StatusLine installed.")
	fmt.Println()
	fmt.Printf("scope:    project-local\n")
	fmt.Printf("settings: %s\n", settingsPath)
	fmt.Printf("command:  xit claude statusline\n")
	fmt.Println()
	fmt.Println("Restart Claude Code to activate the status line.")
	fmt.Println("Use 'xit claude statusline status' to verify.")
	return 0
}

type statusLineLayer struct {
	Path            string
	Exists          bool
	Installed       bool
	Command         string
	CommandAbsolute string
	CommandExists   bool
	CommandExecOK   bool
}

func inspectStatusLineLayer(path string) *statusLineLayer {
	l := &statusLineLayer{Path: path}
	m, err := readRawSettings(path)
	if err != nil {
		return l
	}
	l.Exists = true
	if slRaw, exists := m["statusLine"]; exists {
		l.Installed = true
		var sl map[string]interface{}
		if json.Unmarshal(slRaw, &sl) == nil {
			if cmd, ok := sl["command"].(string); ok {
				l.Command = cmd
				// Extract first word as executable candidate
				fields := strings.Fields(cmd)
				if len(fields) > 0 {
					candidate := fields[0]
					if filepath.IsAbs(candidate) {
						l.CommandAbsolute = candidate
					} else {
						if abs, err := exec.LookPath(candidate); err == nil {
							l.CommandAbsolute = abs
						}
					}
					if _, err := os.Stat(l.CommandAbsolute); err == nil {
						l.CommandExists = true
					}
					// Try exec permission
					if runtime.GOOS != "windows" {
						if info, err := os.Stat(l.CommandAbsolute); err == nil {
							mode := info.Mode()
							l.CommandExecOK = mode&0111 != 0
						}
					} else {
						l.CommandExecOK = l.CommandExists
					}
				}
			}
		}
	}
	return l
}

func cmdClaudeStatuslineStatus() int {
	projectLocal := inspectStatusLineLayer(claudeLocalSettingsPath())
	project := inspectStatusLineLayer(filepath.Join(".claude", "settings.json"))

	userPath := ""
	if homeDir := os.Getenv("HOME"); homeDir != "" {
		userPath = filepath.Join(homeDir, ".claude", "settings.json")
	} else if h, err := os.UserHomeDir(); err == nil {
		userPath = filepath.Join(h, ".claude", "settings.json")
	}
	user := inspectStatusLineLayer(userPath)

	userXiT := ""
	if homeDir := os.Getenv("HOME"); homeDir != "" {
		userXiT = filepath.Join(homeDir, ".xit")
	} else if h, err := os.UserHomeDir(); err == nil {
		userXiT = filepath.Join(h, ".xit")
	}

	hookMode := "unknown"
	reroute := "unknown"
	if userXiT != "" {
		if st, stErr := claudehook.Status(claudehook.ProjectSettingsPath(), userXiT); stErr == nil {
			if st.Installed {
				hookMode = st.Mode
			} else {
				hookMode = "not installed"
			}
			if st.Reroute {
				reroute = "enabled"
			} else {
				reroute = "disabled"
			}
		}
	}

	printLayer := func(name string, l *statusLineLayer) {
		fmt.Printf("%s:\n", name)
		fmt.Printf("  path:             %s\n", l.Path)
		fmt.Printf("  exists:           %v\n", l.Exists)
		fmt.Printf("  installed:        %v\n", l.Installed)
		if l.Installed {
			fmt.Printf("  command:          %s\n", l.Command)
			fmt.Printf("  command_absolute: %s\n", l.CommandAbsolute)
			fmt.Printf("  command_exists:   %v\n", l.CommandExists)
			fmt.Printf("  command_exec_ok:  %v\n", l.CommandExecOK)
		}
	}

	fmt.Println("XiT Claude StatusLine")
	fmt.Println()
	printLayer("project_local", projectLocal)
	fmt.Println()
	printLayer("project", project)
	fmt.Println()
	printLayer("user", user)
	fmt.Println()

	// Guess effective layer
	effective := "unknown"
	if user.Installed {
		effective = "user"
	} else if project.Installed {
		effective = "project"
	} else if projectLocal.Installed {
		effective = "project-local"
	}
	fmt.Printf("effective_guess:  %s\n", effective)
	fmt.Println()
	fmt.Printf("hook_mode:        %s\n", hookMode)
	fmt.Printf("reroute:          %s\n", reroute)
	fmt.Printf("strict:           disabled\n")
	return 0
}

func cmdClaudeStatuslineUninstall(args []string) int {
	yes := false
	for _, a := range args {
		if a == "--yes" {
			yes = true
		}
	}
	if !yes {
		fmt.Fprintln(os.Stderr, "error: uninstall requires --yes")
		return 1
	}

	settingsPath := claudeLocalSettingsPath()
	m, err := readRawSettings(settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", settingsPath, err)
		return 1
	}
	if _, exists := m["statusLine"]; !exists {
		fmt.Printf("statusLine not found in %s\n", settingsPath)
		return 0
	}

	if backupPath, backupErr := claudehook.BackupSettings(settingsPath); backupErr == nil && backupPath != "" {
		fmt.Printf("backup: %s\n", backupPath)
	}

	delete(m, "statusLine")
	if err := writeRawSettings(settingsPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", settingsPath, err)
		return 1
	}

	fmt.Println("XiT Claude StatusLine uninstalled.")
	fmt.Printf("settings: %s\n", settingsPath)
	return 0
}

// ---------- End Claude StatusLine ----------

// ---------- Antigravity StatusLine ----------

const (
	antigravityStatusLineFallback = "吸T神功 · Antigravity · 准备就绪"
	antigravityStatusLineReady    = "吸T神功 · Antigravity · 准备就绪"
)

func antigravitySettingsPath() string {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	return filepath.Join(homeDir, ".gemini", "antigravity-cli", "settings.json")
}

func cmdAntigravityStatusline(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return cmdAntigravityStatuslineInstall(args[1:])
		case "status":
			return cmdAntigravityStatuslineStatus()
		case "uninstall":
			return cmdAntigravityStatuslineUninstall(args[1:])
		}
	}

	useJSON := false
	for _, a := range args {
		if a == "--json" {
			useJSON = true
		}
	}

	line, data := computeAntigravityStatuslineText()

	if useJSON {
		out, _ := json.MarshalIndent(data, "", "  ")
		fmt.Println(string(out))
		return 0
	}

	noColor := os.Getenv("NO_COLOR") != ""
	if noColor {
		fmt.Println(line)
	} else {
		fmt.Printf("%s%s%s\n", statusLineGoldColor, line, statusLineReset)
	}
	return 0
}

func computeAntigravityStatuslineText() (string, map[string]interface{}) {
	// fail-open: any panic returns fallback
	defer func() { recover() }() //nolint

	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	if homeDir == "" {
		return antigravityStatusLineFallback, map[string]interface{}{"line": antigravityStatusLineFallback, "source": "no_home"}
	}
	userXiT := filepath.Join(homeDir, ".xit")
	projectHome := xitHome()
	window := 10 * time.Minute
	now := time.Now()

	// 1. Check autostate for running or recently completed xit auto.
	autoState, autoPath, _ := autostate.Read(projectHome, userXiT)

	var line string
	source := "history"

	switch {
	case autostate.IsRunningFresh(autoState, now):
		line = "吸T神功 · 正在吸T中"
		source = "autostate_running"
	case autostate.IsCompletedFresh(autoState, now) && autoState != nil && autoState.SavedBytes > 0:
		line = fmt.Sprintf("吸T神功 · 本次省%s Token", formatTokenCount(int(autoState.SavedBytes/4)))
		source = "autostate_completed"
	default:
		// Recent hitrate (10 min window). For Antigravity this is history-only.
		report, _ := hitrate.ComputeReportForAdapter("antigravity", userXiT, projectHome, window)
		hasRecentEvents := report != nil && report.ShellCommandsSeen > 0
		hitRatePct := 0.0
		if report != nil && (report.ShouldCompress.Total+report.ShouldPassthrough.Total) > 0 {
			total := report.ShouldCompress.Total + report.ShouldPassthrough.Total
			correct := report.ShouldCompress.CorrectlyWrapped + report.ShouldPassthrough.CorrectlyPassthrough
			hitRatePct = float64(correct) / float64(total) * 100
		}

		// Recent token savings from xit auto history (10 min).
		savedTokens := 0
		if m, err := history.ComputeSessionMetrics(projectHome, window); err == nil && m != nil {
			if m.CurrentSession.SavedBytes > 0 {
				savedTokens = m.CurrentSession.SavedBytes / 4
			}
		}
		if savedTokens == 0 {
			if m, err := history.ComputeSessionMetrics(userXiT, window); err == nil && m != nil {
				if m.CurrentSession.SavedBytes > 0 {
					savedTokens = m.CurrentSession.SavedBytes / 4
				}
			}
		}

		if savedTokens > 0 {
			line = fmt.Sprintf("吸T神功 · 本次省%s Token", formatTokenCount(savedTokens))
		} else if hasRecentEvents {
			line = antigravityStatusLineReady
		} else {
			line = antigravityStatusLineFallback
		}

		verdictStr := ""
		if report != nil {
			verdictStr = report.Verdict
		}
		data := map[string]interface{}{
			"line":                line,
			"color":               "gold",
			"hit_rate":            hitRatePct,
			"saved_tokens_recent": savedTokens,
			"has_recent_events":   hasRecentEvents,
			"verdict":             verdictStr,
			"source":              source,
			"autostate_path":      autoPath,
		}
		return line, data
	}

	data := map[string]interface{}{
		"line":           line,
		"color":          "gold",
		"source":         source,
		"autostate_path": autoPath,
	}
	if autoState != nil {
		data["autostate_status"] = autoState.Status
		data["autostate_saved_bytes"] = autoState.SavedBytes
	}
	return line, data
}

func cmdAntigravityStatuslineInstall(args []string) int {
	scope := "user"
	yes := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--yes":
			yes = true
		case args[i] == "--scope" && i+1 < len(args):
			scope = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--scope="):
			scope = strings.TrimPrefix(args[i], "--scope=")
		}
	}

	if scope != "user" {
		fmt.Fprintf(os.Stderr, "xit antigravity statusline install: only --scope user is supported\n")
		return 1
	}

	settingsPath := antigravitySettingsPath()
	m, err := readRawSettings(settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", settingsPath, err)
		return 1
	}

	// Check if already installed.
	if _, exists := m["statusLine"]; exists && !yes {
		fmt.Printf("XiT Antigravity StatusLine already present in %s\n", settingsPath)
		fmt.Println("Use --yes to overwrite.")
		return 1
	}

	// Backup if file exists.
	if _, statErr := os.Stat(settingsPath); statErr == nil {
		if backupPath, backupErr := claudehook.BackupSettings(settingsPath); backupErr == nil && backupPath != "" {
			fmt.Printf("backup: %s\n", backupPath)
		}
	}

	// Resolve absolute path to xit for reliable execution inside Antigravity CLI.
	xitPath, lookErr := exec.LookPath("xit")
	if lookErr != nil || xitPath == "" {
		candidates := []string{
			filepath.Join(os.Getenv("HOME"), ".local", "bin", "xit"),
			"/usr/local/bin/xit",
			"/opt/homebrew/bin/xit",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				xitPath = c
				break
			}
		}
	}
	if xitPath == "" {
		fmt.Fprintln(os.Stderr, "cannot find xit in PATH; ensure xit is installed and in PATH before installing statusLine")
		return 1
	}

	// Build statusLine value.
	statusLineVal := map[string]interface{}{
		"type":    "command",
		"command": xitPath + " antigravity statusline",
		"padding": 0,
	}
	slData, _ := json.Marshal(statusLineVal)
	m["statusLine"] = json.RawMessage(slData)

	if err := writeRawSettings(settingsPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", settingsPath, err)
		return 1
	}

	fmt.Println("XiT Antigravity StatusLine installed.")
	fmt.Println()
	fmt.Printf("scope:    user\n")
	fmt.Printf("settings: %s\n", settingsPath)
	fmt.Printf("command:  xit antigravity statusline\n")
	fmt.Println()
	fmt.Println("Restart Antigravity CLI to activate the status line.")
	fmt.Println("Use 'xit antigravity statusline status' to verify.")
	return 0
}

func cmdAntigravityStatuslineStatus() int {
	settingsPath := antigravitySettingsPath()
	layer := inspectStatusLineLayer(settingsPath)

	fmt.Println("XiT Antigravity StatusLine")
	fmt.Println()
	fmt.Printf("settings:\n")
	fmt.Printf("  path:             %s\n", layer.Path)
	fmt.Printf("  exists:           %v\n", layer.Exists)
	fmt.Printf("  installed:        %v\n", layer.Installed)
	if layer.Installed {
		fmt.Printf("  command:          %s\n", layer.Command)
		fmt.Printf("  command_absolute: %s\n", layer.CommandAbsolute)
		fmt.Printf("  command_exists:   %v\n", layer.CommandExists)
		fmt.Printf("  command_exec_ok:  %v\n", layer.CommandExecOK)
	}
	fmt.Println()
	fmt.Printf("hook_mode:        not available (no hooks for Antigravity)\n")
	fmt.Printf("reroute:          disabled\n")
	fmt.Printf("strict:           disabled\n")
	return 0
}

func cmdAntigravityStatuslineUninstall(args []string) int {
	yes := false
	for _, a := range args {
		if a == "--yes" {
			yes = true
		}
	}
	if !yes {
		fmt.Fprintln(os.Stderr, "error: uninstall requires --yes")
		return 1
	}

	settingsPath := antigravitySettingsPath()
	m, err := readRawSettings(settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading %s: %v\n", settingsPath, err)
		return 1
	}
	if _, exists := m["statusLine"]; !exists {
		fmt.Printf("statusLine not found in %s\n", settingsPath)
		return 0
	}

	if backupPath, backupErr := claudehook.BackupSettings(settingsPath); backupErr == nil && backupPath != "" {
		fmt.Printf("backup: %s\n", backupPath)
	}

	delete(m, "statusLine")
	if err := writeRawSettings(settingsPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", settingsPath, err)
		return 1
	}

	fmt.Println("XiT Antigravity StatusLine uninstalled.")
	fmt.Printf("settings: %s\n", settingsPath)
	return 0
}

// ---------- End Antigravity StatusLine ----------

func cmdClaudeHitrate(args []string) int {
	window := 2 * time.Hour
	useJSON := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			useJSON = true
		case strings.HasPrefix(a, "--last"):
			var val string
			if strings.Contains(a, "=") {
				val = strings.SplitN(a, "=", 2)[1]
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
			if d, err := time.ParseDuration(val); err == nil {
				window = d
			}
		}
	}

	projectHome := xitHome()
	actualUserHomeDir := os.Getenv("HOME")
	if actualUserHomeDir == "" {
		var err error
		actualUserHomeDir, err = os.UserHomeDir()
		if err != nil {
			actualUserHomeDir = "."
		}
	}
	userHome := filepath.Join(actualUserHomeDir, ".xit")

	report, err := hitrate.ComputeReportForAdapter("claude", userHome, projectHome, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if useJSON {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	fmt.Print(hitrate.FormatReport(report, false))
	return 0
}

func cmdCodexHitrate(args []string) int {
	window := 2 * time.Hour
	useJSON := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			useJSON = true
		case strings.HasPrefix(a, "--last"):
			var val string
			if strings.Contains(a, "=") {
				val = strings.SplitN(a, "=", 2)[1]
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
			if d, err := time.ParseDuration(val); err == nil {
				window = d
			}
		}
	}

	projectHome := xitHome()
	actualUserHomeDir := os.Getenv("HOME")
	if actualUserHomeDir == "" {
		var err error
		actualUserHomeDir, err = os.UserHomeDir()
		if err != nil {
			actualUserHomeDir = "."
		}
	}
	userHome := filepath.Join(actualUserHomeDir, ".xit")

	report, err := hitrate.ComputeReportForAdapter("codex", userHome, projectHome, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if useJSON {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	fmt.Print(hitrate.FormatReport(report, false))
	return 0
}

func cmdClaudeHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: xit claude-hook pretooluse-bash")
	}
	sub := args[0]
	switch sub {
	case "pretooluse-bash":
		home := userXiTHome()
		return claudehook.RunHookCommand(home)
	default:
		return fmt.Errorf("unknown claude-hook command: %s", sub)
	}
}

func cmdKimiHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: xit kimi-hook observe|turn")
	}
	sub := args[0]
	switch sub {
	case "observe":
		home := userXiTHome()
		return kimihook.RunHookCommand(home)
	case "turn":
		home := userXiTHome()
		return kimihook.RunTurnHookCommand(home, args[1:])
	default:
		return fmt.Errorf("unknown kimi-hook command: %s", sub)
	}
}

func cmdCodexHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: xit codex-hook pretooluse-bash")
	}
	sub := args[0]
	switch sub {
	case "pretooluse-bash":
		home := userXiTHome()
		return codexhook.RunHookCommand(home)
	default:
		return fmt.Errorf("unknown codex-hook command: %s", sub)
	}
}

func cmdKimiTurnStatus(args []string) int {
	useJSON := false
	for _, a := range args {
		if a == "--json" {
			useJSON = true
		}
	}

	home := userXiTHome()
	res := kimihook.ReadTurnStatus(home)

	if useJSON {
		data, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(data))
		return 0
	}

	fmt.Println("XiT Kimi Turn Status")
	fmt.Println()
	fmt.Printf("state_file:         %s\n", res.StateFile)
	fmt.Printf("fallback_state_file: %s\n", res.FallbackStateFile)
	fmt.Printf("source:             %s\n", res.Source)
	fmt.Println()
	fmt.Println("current_turn:")
	fmt.Printf("  status:      %s\n", res.CurrentTurn.Status)
	fmt.Printf("  event:       %s\n", res.CurrentTurn.Event)
	fmt.Printf("  started_at:  %s\n", res.CurrentTurn.StartedAt)
	fmt.Printf("  finished_at: %s\n", res.CurrentTurn.FinishedAt)
	if res.CurrentTurn.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339, res.CurrentTurn.StartedAt); err == nil {
			fmt.Printf("  age:         %s\n", time.Since(t).Round(time.Second))
		}
	}
	fmt.Printf("  session_id:  %s\n", res.CurrentTurn.SessionID)
	fmt.Printf("  cwd:         %s\n", res.CurrentTurn.Cwd)
	fmt.Println()
	fmt.Println("auto_state:")
	fmt.Printf("  status:      %s\n", res.AutoState.Status)
	fmt.Printf("  command:     %s\n", res.AutoState.Command)
	fmt.Printf("  saved_bytes: %d\n", res.AutoState.SavedBytes)
	fmt.Printf("  saved_tokens: %d\n", res.AutoState.SavedBytes/4)
	fmt.Printf("  token_method: saved_bytes / 4\n")
	fmt.Printf("  raw_log:     %s\n", res.AutoState.RawLog)
	fmt.Println()
	fmt.Println("turn_scope:")
	fmt.Printf("  auto_commands: %d\n", res.TurnStats.AutoCount)
	fmt.Printf("  saved_bytes: %d\n", res.TurnStats.SavedBytes)
	fmt.Printf("  saved_tokens: %d\n", res.TurnStats.SavedTokens)
	fmt.Printf("  token_method: saved_bytes / 4\n")
	fmt.Println()
	fmt.Printf("toolbar_expected: %s\n", res.ToolbarExpected)
	return 0
}

func cmdKimiTurnDiagnose(args []string) int {
	useJSON := false
	for _, a := range args {
		if a == "--json" {
			useJSON = true
		}
	}
	home := userXiTHome()
	kimihook.RunTurnDiagnose(home, useJSON)
	return 0
}

func cmdHook(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: xit hook status|stats|hitrate|install|uninstall <target>")
	}
	sub := args[0]
	target := args[1]
	home := userXiTHome()

	switch target {
	case "claude":
		settingsPath := claudehook.ResolveSettingsPath("project")

		switch sub {
		case "status":
			status, err := claudehook.Status(settingsPath, home)
			if err != nil {
				return err
			}
			fmt.Println("XiT Claude Hook Status")
			fmt.Println()
			fmt.Printf("scope:      project\n")
			fmt.Printf("settings:   %s\n", status.SettingsPath)
			if status.Installed {
				fmt.Printf("installed:  yes\n")
				fmt.Printf("hook:       PreToolUse/Bash\n")
				fmt.Printf("script:     %s\n", status.ScriptPath)
			} else {
				fmt.Printf("installed:  no\n")
			}
			fmt.Printf("mode:       %s\n", status.Mode)
			if status.Reroute {
				fmt.Printf("reroute:    enabled\n")
			} else {
				fmt.Printf("reroute:    disabled\n")
			}
			fmt.Printf("rewrite:    disabled\n")
			fmt.Printf("fail_open:  yes\n")
			if status.HasEvents {
				fmt.Printf("events:     %s\n", filepath.Join(home, "claude-hooks", "events.jsonl"))
			}
			return nil
		case "enable-reroute":
			if !hasYesFlag(args[2:]) {
				return fmt.Errorf("enable-reroute requires --yes to confirm")
			}
			if err := claudehook.EnableReroute(home); err != nil {
				return err
			}
			fmt.Println("XiT Claude safe reroute enabled.")
			fmt.Println("Claude Code will now receive deny recommendations for high-output Bash commands.")
			return nil
		case "disable-reroute":
			if !hasYesFlag(args[2:]) {
				return fmt.Errorf("disable-reroute requires --yes to confirm")
			}
			if err := claudehook.DisableReroute(home); err != nil {
				return err
			}
			fmt.Println("XiT Claude safe reroute disabled. Back to observe mode.")
			return nil
		case "stats":
			stats, err := claudehook.Stats(home)
			if err != nil {
				return err
			}
			fmt.Println("XiT Claude Hook Stats")
			fmt.Println()
			if !stats.HasEvents {
				fmt.Println("No hook events recorded yet.")
				fmt.Println("Events are logged to .xit/claude-hooks/events.jsonl when Claude Code runs Bash commands.")
				return nil
			}
			fmt.Printf("events:      %d\n", stats.Events)
			fmt.Printf("observed:    %d\n", stats.Observed)
			fmt.Printf("rerouted:    %d\n", stats.Rerouted)
			fmt.Printf("passthrough: %d\n", stats.Passthrough)
			fmt.Printf("errors:      %d\n", stats.Errors)
			if len(stats.TopCommands) > 0 {
				fmt.Println("\ntop commands:")
				sort.Slice(stats.TopCommands, func(i, j int) bool {
					return stats.TopCommands[i].Count > stats.TopCommands[j].Count
				})
				for _, tc := range stats.TopCommands {
					fmt.Printf("- %-30s rerouted %d\n", tc.Command, tc.Count)
				}
			}
			return nil
		case "hitrate":
			os.Exit(cmdClaudeHitrate(args[2:]))
			return nil
		default:
			return fmt.Errorf("unknown hook command: %s", sub)
		}
	case "kimi":
		scope, restArgs := extractScopeFlag(args[2:])
		configPath := kimihook.ResolveConfigPath(scope)
		switch sub {
		case "status":
			_ = restArgs
			status, err := kimihook.Status(configPath, home)
			if err != nil {
				return err
			}
			fmt.Println("XiT Kimi Hook Status")
			fmt.Println()
			fmt.Printf("scope:      %s\n", scope)
			fmt.Printf("config:     %s\n", status.ConfigPath)

			format := status.Format
			var legacyPath string
			if scope == "project" {
				legacyPath = kimihook.LegacyProjectConfigPath()
			} else {
				legacyPath = kimihook.LegacyUserConfigPath()
			}
			if format == kimihook.FormatNone && legacyPath != "" {
				if _, err := os.Stat(legacyPath); err == nil {
					format = kimihook.FormatJSON
				}
			}

			if status.Installed {
				fmt.Printf("installed:  yes\n")
				fmt.Printf("hook:       PreToolUse/Shell+Bash (beta)\n")
				if format == kimihook.FormatJSON {
					fmt.Printf("format:     json legacy\n")
				} else {
					fmt.Printf("format:     %s\n", format)
				}
				fmt.Printf("script:     %s\n", status.ScriptPath)
			} else {
				fmt.Printf("installed:  no\n")
				if format == kimihook.FormatJSON {
					fmt.Printf("format:     json legacy\n")
				} else if format != kimihook.FormatNone {
					fmt.Printf("format:     %s\n", format)
				}
			}
			if format == kimihook.FormatJSON {
				fmt.Println()
				fmt.Println("warning: JSON config may not be loaded by current Kimi CLI; TOML is recommended.")
			}
			if status.HasConflict {
				fmt.Println()
				fmt.Println("warning: config contains both hooks = [] and [[hooks]]; this may prevent Kimi from loading hooks.")
			}
			fmt.Printf("mode:       %s\n", status.Mode)
			if status.Reroute {
				fmt.Printf("reroute:    enabled\n")
			} else {
				fmt.Printf("reroute:    disabled\n")
			}
			if status.InlineStatus {
				fmt.Printf("reroute_notice: enabled\n")
			} else {
				fmt.Printf("reroute_notice: disabled\n")
			}
			fmt.Printf("notice_style: %s\n", status.StatusStyle)
			fmt.Printf("persistent_status_bar: not implemented\n")
			fmt.Printf("rewrite:    disabled\n")
			fmt.Printf("fail_open:  yes\n")
			fmt.Printf("turn_lifecycle: %v\n", boolToYesNo(status.TurnLifecycle))
			fmt.Println("turn_scripts:")
			fmt.Printf("  UserPromptSubmit: %s\n", scriptStatusLabel(status.TurnScripts["UserPromptSubmit"]))
			fmt.Printf("  Stop:             %s\n", scriptStatusLabel(status.TurnScripts["Stop"]))
			fmt.Printf("  SessionStart:     %s\n", scriptStatusLabel(status.TurnScripts["SessionStart"]))
			fmt.Printf("  SessionEnd:       %s\n", scriptStatusLabel(status.TurnScripts["SessionEnd"]))
			fmt.Println("events:")
			fmt.Printf("  UserPromptSubmit: %s\n", boolToYesNo(status.TurnEvents["UserPromptSubmit"]))
			fmt.Printf("  Stop:             %s\n", boolToYesNo(status.TurnEvents["Stop"]))
			fmt.Printf("  SessionStart:     %s\n", boolToYesNo(status.TurnEvents["SessionStart"]))
			fmt.Printf("  SessionEnd:       %s\n", boolToYesNo(status.TurnEvents["SessionEnd"]))
			if status.HasEvents {
				fmt.Printf("events:     %s\n", filepath.Join(home, "kimi-hooks", "events.jsonl"))
			}
			return nil
		case "enable-reroute":
			if !hasYesFlag(restArgs) {
				return fmt.Errorf("enable-reroute requires --yes to confirm")
			}
			if err := kimihook.EnableReroute(home); err != nil {
				return err
			}
			fmt.Println("XiT Kimi reroute enabled.")
			fmt.Println()
			fmt.Println("Important:")
			fmt.Println("Kimi currently shows deny responses as Shell tool errors.")
			fmt.Println("Kimi may not automatically rerun `xit auto <command>`.")
			fmt.Println("This mode is useful for testing and explicit guidance, not a true persistent status bar.")
			return nil
		case "disable-reroute":
			if !hasYesFlag(restArgs) {
				return fmt.Errorf("disable-reroute requires --yes to confirm")
			}
			if err := kimihook.DisableReroute(home); err != nil {
				return err
			}
			fmt.Println("XiT Kimi safe reroute disabled. Back to observe mode.")
			return nil
		case "status-style":
			if len(restArgs) < 1 {
				return fmt.Errorf("usage: xit hook status-style kimi compact|detailed --yes")
			}
			style := restArgs[0]
			if style != "compact" && style != "detailed" {
				return fmt.Errorf("status-style must be compact or detailed")
			}
			if !hasYesFlag(restArgs[1:]) {
				return fmt.Errorf("status-style requires --yes to confirm")
			}
			if err := kimihook.SetStatusStyle(home, style); err != nil {
				return err
			}
			fmt.Printf("XiT Kimi reroute notice style set to %s.\n", style)
			return nil
		case "stats":
			stats, err := kimihook.Stats(home)
			if err != nil {
				return err
			}
			fmt.Println("XiT Kimi Hook Stats")
			fmt.Println()
			if !stats.HasEvents {
				fmt.Println("No hook events recorded yet.")
				fmt.Println("Events are logged to .xit/kimi-hooks/events.jsonl when Kimi runs Shell/Bash commands.")
				return nil
			}
			fmt.Printf("events:      %d\n", stats.Events)
			fmt.Printf("observed:    %d\n", stats.Observed)
			fmt.Printf("rerouted:    %d\n", stats.Rerouted)
			fmt.Printf("passthrough: %d\n", stats.Passthrough)
			fmt.Printf("errors:      %d\n", stats.Errors)
			if len(stats.TopCommands) > 0 {
				fmt.Println("\ntop commands:")
				sort.Slice(stats.TopCommands, func(i, j int) bool {
					return stats.TopCommands[i].Count > stats.TopCommands[j].Count
				})
				for _, tc := range stats.TopCommands {
					fmt.Printf("- %-30s rerouted %d\n", tc.Command, tc.Count)
				}
			}
			return nil
		case "test":
			return cmdKimiHookTest(home)
		default:
			return fmt.Errorf("unknown hook command for kimi: %s", sub)
		}
	case "codex":
		scope, restArgs := extractScopeFlag(args[2:])
		projectPath, _ := os.Getwd()
		if scope != "project" {
			return fmt.Errorf("codex hooks only support project scope")
		}
		switch sub {
		case "status":
			status, err := codexhook.Status(projectPath, home)
			if err != nil {
				return err
			}
			fmt.Println("XiT Codex Hook Status")
			fmt.Println()
			fmt.Printf("scope:      project\n")
			fmt.Printf("hooks:      %s\n", status.HooksPath)
			if status.Installed {
				fmt.Printf("installed:  yes\n")
				fmt.Printf("hook:       PreToolUse/Bash\n")
				fmt.Printf("script:     %s\n", status.ScriptPath)
			} else {
				fmt.Printf("installed:  no\n")
			}
			fmt.Printf("mode:       %s\n", status.Mode)
			fmt.Printf("reroute:    disabled\n")
			fmt.Printf("rewrite:    disabled\n")
			fmt.Printf("fail_open:  yes\n")
			if status.HasEvents {
				fmt.Printf("events:     %s\n", filepath.Join(home, "codex-hooks", "events.jsonl"))
			}
			return nil
		case "install":
			if !hasYesFlag(restArgs) {
				return fmt.Errorf("install requires --yes to confirm")
			}
			res, err := codexhook.Install(projectPath, home, false)
			if err != nil {
				return err
			}
			if res.AlreadyInstalled {
				fmt.Println("XiT Codex hook already installed.")
			} else {
				fmt.Println("XiT Codex hook installed.")
			}
			fmt.Printf("hooks:   %s\n", res.HooksPath)
			fmt.Printf("script:  %s\n", res.ScriptPath)
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  1. Launch Codex with hooks enabled:")
			fmt.Println("     codex --enable hooks")
			fmt.Println("  2. On first launch, approve/trust the hook if Codex prompts.")
			fmt.Println("  3. Verify: xit hook stats codex")
			fmt.Println()
			fmt.Println("Note: Codex does not support command-backed bottom statusLine.")
			fmt.Println("      This hook provides observe/hitrate only (no reroute).")
			return nil
		case "uninstall":
			if !hasYesFlag(restArgs) {
				return fmt.Errorf("uninstall requires --yes to confirm")
			}
			if err := codexhook.Uninstall(projectPath); err != nil {
				return err
			}
			fmt.Println("XiT Codex hook uninstalled.")
			return nil
		case "stats":
			stats, err := codexhook.Stats(home)
			if err != nil {
				return err
			}
			fmt.Println("XiT Codex Hook Stats")
			fmt.Println()
			if !stats.HasEvents {
				fmt.Println("No hook events recorded yet.")
				fmt.Println("Events are logged to .xit/codex-hooks/events.jsonl when Codex runs Bash commands.")
				return nil
			}
			fmt.Printf("events:      %d\n", stats.Events)
			fmt.Printf("observed:    %d\n", stats.Observed)
			fmt.Printf("passthrough: %d\n", stats.Passthrough)
			fmt.Printf("errors:      %d\n", stats.Errors)
			return nil
		case "hitrate":
			os.Exit(cmdCodexHitrate(args[2:]))
			return nil
		default:
			return fmt.Errorf("unknown hook command for codex: %s", sub)
		}
	default:
		return fmt.Errorf("hook commands only supported for claude, kimi, and codex")
	}
}

func hasYesFlag(args []string) bool {
	for _, a := range args {
		if a == "--yes" {
			return true
		}
	}
	return false
}

func hasDeepFlag(args []string) bool {
	for _, a := range args {
		if a == "--deep" {
			return true
		}
	}
	return false
}

func extractScopeFlag(args []string) (string, []string) {
	scope := "project"
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--scope=") {
			scope = strings.TrimPrefix(args[i], "--scope=")
			continue
		}
		rest = append(rest, args[i])
	}
	return scope, rest
}

func cmdUninstall(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: xit uninstall <target> --method official_hook --yes [--scope project|user]")
	}
	target := args[0]
	method := ""
	scope := "project"
	yes := false
	for i := 1; i < len(args); i++ {
		if args[i] == "--method" && i+1 < len(args) {
			method = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--method=") {
			method = strings.TrimPrefix(args[i], "--method=")
			continue
		}
		if args[i] == "--scope" && i+1 < len(args) {
			scope = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--scope=") {
			scope = strings.TrimPrefix(args[i], "--scope=")
			continue
		}
		if args[i] == "--yes" {
			yes = true
		}
	}
	if method == "" {
		return fmt.Errorf("--method is required")
	}
	if !yes {
		return fmt.Errorf("uninstall requires --yes to confirm")
	}

	a, ok := integrations.Registry[target]
	if !ok {
		return fmt.Errorf("unknown target: %s", target)
	}

	home := userXiTHome()
	var cfg *config.Config
	if config.Exists(home) {
		var err error
		cfg, err = config.Load(home)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("config not found. Run: xit init %s", target)
	}

	// For Kimi official_hook, explicitly uninstall from specified scope first.
	if target == "kimi" && method == "official_hook" {
		configPath := kimihook.ResolveConfigPath(scope)
		if err := kimihook.Uninstall(configPath, home, false); err != nil {
			if !strings.Contains(err.Error(), "not found") {
				return err
			}
		}
	}

	if err := a.Uninstall(home, cfg, true); err != nil {
		return err
	}
	fmt.Printf("XiT %s uninstalled.\n", target)
	return nil
}

func boolToYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func scriptStatusLabel(exists bool) string {
	if exists {
		return "exists/executable"
	}
	return "missing"
}

func boolToOkFail(v bool) string {
	if v {
		return "ok"
	}
	return "fail"
}
