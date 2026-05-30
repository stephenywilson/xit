package integrations

import (
	"fmt"
	"strings"

	"github.com/stephenywilson/xit/internal/config"
	"github.com/stephenywilson/xit/internal/kimihook"
)

type kimiAdapter struct{}

func (a *kimiAdapter) base() *baseAdapter {
	return &baseAdapter{
		name:        "kimi",
		displayName: "Kimi",
		commands:    []string{"kimi"},
		recommended: MethodManual,
		supported:   []Method{MethodManual, MethodOfficialHook, MethodSessionShim, MethodWrapper},
		note:        "Kimi TUI is not currently safe for XiT PTY takeover. Use manual compression unless testing --unsafe-pty. Kimi hooks are beta (TOML config, Shell+Bash matchers); observe mode only.",
	}
}

func (a *kimiAdapter) Name() string                { return a.base().Name() }
func (a *kimiAdapter) DisplayName() string         { return a.base().DisplayName() }
func (a *kimiAdapter) Commands() []string          { return a.base().Commands() }
func (a *kimiAdapter) Detect() TargetStatus        { return a.base().Detect() }
func (a *kimiAdapter) Doctor(cfg *config.Config) DoctorResult {
	result := a.base().Doctor(cfg)
	// If official hook is installed, reflect it.
	if cfg != nil {
		if t, ok := cfg.Targets["kimi"]; ok && t.Enabled && t.Integration == "official_hook" {
			result.Installed = "official_hook_beta"
		}
	}
	return result
}

func (a *kimiAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	status := a.Detect()
	selected := a.base().recommended
	if method != "" {
		selected = Method(method)
	}

	actions := []string{}
	canInstall := false
	safeOption := ""
	note := a.base().note

	switch selected {
	case MethodOfficialHook:
		configPath := kimihook.ResolveConfigPath("project")
		res, err := kimihook.Install(configPath, kimihook.XiTHome(), true)
		_ = res
		_ = err
		actions = append(actions,
			"Locate Kimi config directory",
			"Write TOML config with [[hooks]] entries",
			"Install Shell + Bash matchers for compatibility",
			"Hook will observe tool calls and log events locally",
			"No command rewrite",
			"No prompt/response modification",
			"No telemetry",
		)
		note = "Kimi official hook is beta. Uses TOML config with Shell+Bash matchers. Verify with Kimi docs before relying on this in production."
		canInstall = true
		safeOption = fmt.Sprintf("xit init %s --method official_hook --scope project --yes", a.base().name)
	case MethodSessionShim:
		actions = append(actions,
			"Session auto command shims are already available via xit session",
			"Run: xit session "+a.base().name,
		)
		canInstall = false
		safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", a.base().name)
	case MethodWrapper:
		actions = append(actions,
			"Enable wrapper in XiT config",
			fmt.Sprintf("Run: xit %s", a.base().name),
		)
		canInstall = true
		safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", a.base().name)
	case MethodManual:
		actions = append(actions,
			"No automatic integration",
			"Run commands manually through: xit --mode agent <command>",
		)
		canInstall = false
	}

	// If selected method is not supported, fallback to wrapper if available.
	supported := false
	for _, m := range a.base().supported {
		if m == selected {
			supported = true
			break
		}
	}
	if !supported {
		for _, m := range a.base().supported {
			if m == MethodWrapper {
				selected = MethodWrapper
				actions = []string{
					"Selected method not supported for this target",
					"Falling back to wrapper",
					"Enable wrapper in XiT config",
					fmt.Sprintf("Run: xit %s", a.base().name),
				}
				canInstall = true
				safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", a.base().name)
				break
			}
		}
	}

	return InstallPlan{
		Target:            a.base().name,
		Detected:          status.Detected,
		Path:              status.Path,
		RecommendedMethod: a.base().recommended,
		SelectedMethod:    selected,
		SupportedMethods:  a.base().supported,
		Actions:           actions,
		SafeOption:        safeOption,
		CanInstall:        canInstall,
		Note:              note,
	}
}

func (a *kimiAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	if plan.SelectedMethod == MethodOfficialHook {
		if !yes {
			return fmt.Errorf("install requires --yes to confirm")
		}
		scope := plan.Scope
		if scope == "" {
			scope = "project"
		}
		configPath := kimihook.ResolveConfigPath(scope)
		_, err := kimihook.Install(configPath, home, false)
		if err != nil {
			return err
		}
		t, ok := cfg.Targets["kimi"]
		if !ok {
			return fmt.Errorf("unknown target: kimi")
		}
		path := plan.Path
		if path == "" {
			path = config.DetectPath("kimi")
		}
		t.Enabled = true
		t.Path = path
		t.Integration = "official_hook"
		cfg.Targets["kimi"] = t
		return config.Save(home, cfg)
	}
	return a.base().Install(home, cfg, plan, yes)
}

func (a *kimiAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	if !yes {
		return fmt.Errorf("uninstall requires --yes to confirm")
	}
	t, ok := cfg.Targets["kimi"]
	if !ok {
		return fmt.Errorf("unknown target: kimi")
	}
	if t.Integration == "official_hook" {
		configPath := kimihook.ResolveConfigPath("project")
		err := kimihook.Uninstall(configPath, home, false)
		if err != nil {
			if !strings.Contains(err.Error(), "not found") {
				return err
			}
			// Try user scope as fallback.
			userPath := kimihook.ResolveConfigPath("user")
			if err := kimihook.Uninstall(userPath, home, false); err != nil {
				if !strings.Contains(err.Error(), "not found") {
					return err
				}
			}
		}
	}
	t.Enabled = false
	t.Integration = "manual"
	cfg.Targets["kimi"] = t
	return config.Save(home, cfg)
}

func (a *kimiAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return a.base().Launch(home, cfg, args, mode)
}
