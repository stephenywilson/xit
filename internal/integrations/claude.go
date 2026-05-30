package integrations

import (
	"fmt"

	"github.com/stephenywilson/xit/internal/claudehook"
	"github.com/stephenywilson/xit/internal/config"
)

type claudeAdapter struct{}

func (a *claudeAdapter) base() *baseAdapter {
	return &baseAdapter{
		name:        "claude",
		displayName: "Claude Code",
		commands:    []string{"claude"},
		recommended: MethodOfficialHook,
		supported:   []Method{MethodOfficialHook, MethodSessionShim, MethodWrapper},
		note:        "Claude Code supports lifecycle hooks; official hook should be preferred.",
	}
}

func (a *claudeAdapter) Name() string                { return a.base().Name() }
func (a *claudeAdapter) DisplayName() string         { return a.base().DisplayName() }
func (a *claudeAdapter) Commands() []string          { return a.base().Commands() }
func (a *claudeAdapter) Detect() TargetStatus        { return a.base().Detect() }

func (a *claudeAdapter) Doctor(cfg *config.Config) DoctorResult {
	result := a.base().Doctor(cfg)
	home := claudehook.XiTHome()
	settingsPath := claudehook.ResolveSettingsPath("project")
	status, err := claudehook.Status(settingsPath, home)
	if err == nil && status.Installed {
		result.Installed = "official_hook"
		result.Note = fmt.Sprintf("official_hook installed (%s, observe mode, fail-open)", settingsPath)
	}
	return result
}

func (a *claudeAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	plan := a.base().PlanInstall(home, cfg, method)
	if plan.SelectedMethod == MethodOfficialHook {
		if plan.Detected {
			plan.CanInstall = true
			plan.Actions = []string{
				"Detect Claude Code settings path (project: .claude/settings.json)",
				"Backup existing settings before modification",
				"Merge XiT PreToolUse Bash hook into settings.json",
				"Create hook script: .xit/hooks/claude-pretooluse-bash.sh",
				"Hook runs in observe mode: logs events, does not block or rewrite",
			}
			plan.Note = "Claude Code official hook: observe mode, fail-open, no blocking by default."
			plan.SafeOption = "xit init claude --method official_hook --scope project --yes"
		} else {
			plan.CanInstall = false
			plan.Note = "Claude Code not detected in PATH. Install Claude Code first."
		}
	}
	return plan
}

func (a *claudeAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	if plan.SelectedMethod == MethodOfficialHook {
		scope := plan.Scope
		if scope == "" {
			scope = "project"
		}
		settingsPath := claudehook.ResolveSettingsPath(scope)
		_, err := claudehook.Install(settingsPath, home, false)
		if err != nil {
			return err
		}
		t := cfg.Targets["claude"]
		t.Enabled = true
		t.Path = plan.Path
		t.Integration = "official_hook"
		cfg.Targets["claude"] = t
		return config.Save(home, cfg)
	}
	return a.base().Install(home, cfg, plan, yes)
}

func (a *claudeAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	t := cfg.Targets["claude"]
	if t.Integration == "official_hook" {
		scope := "project"
		settingsPath := claudehook.ResolveSettingsPath(scope)
		if err := claudehook.Uninstall(settingsPath, home, false); err != nil {
			return err
		}
		t.Enabled = false
		t.Integration = "manual"
		cfg.Targets["claude"] = t
		return config.Save(home, cfg)
	}
	return a.base().Uninstall(home, cfg, yes)
}

func (a *claudeAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return a.base().Launch(home, cfg, args, mode)
}
