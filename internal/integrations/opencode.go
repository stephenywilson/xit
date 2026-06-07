package integrations

import (
	"fmt"
	"os"

	"github.com/stephenywilson/xit/internal/config"
	"github.com/stephenywilson/xit/internal/opencodehook"
)

type opencodeAdapter struct{}

func (a *opencodeAdapter) base() *baseAdapter {
	return &baseAdapter{
		name:        "opencode",
		displayName: "OpenCode",
		commands:    []string{"opencode"},
		recommended: MethodOfficialHook,
		supported:   []Method{MethodOfficialHook, MethodSessionShim, MethodWrapper, MethodManual},
		note:        "OpenCode supports plugins via .opencode/plugins/*.ts; official plugin should be preferred.",
	}
}

func (a *opencodeAdapter) Name() string                { return a.base().Name() }
func (a *opencodeAdapter) DisplayName() string         { return a.base().DisplayName() }
func (a *opencodeAdapter) Commands() []string          { return a.base().Commands() }
func (a *opencodeAdapter) Detect() TargetStatus        { return a.base().Detect() }

func (a *opencodeAdapter) Doctor(cfg *config.Config) DoctorResult {
	result := a.base().Doctor(cfg)
	home := opencodehook.XiTHome()
	projectDir, _ := os.Getwd()
	status, err := opencodehook.Status(projectDir, home)
	if err == nil && status.Installed {
		result.Installed = "official_hook"
		result.Note = fmt.Sprintf("official_hook installed (%s)", status.PluginPath)
	}
	return result
}

func (a *opencodeAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	plan := a.base().PlanInstall(home, cfg, method)
	if plan.SelectedMethod == MethodOfficialHook {
		if plan.Detected {
			plan.CanInstall = true
			plan.Actions = []string{
				"Detect OpenCode in PATH",
				"Write XiT plugin to .opencode/plugins/xit.ts",
				"Plugin listens to tool.execute.before/after for Bash commands",
				"High-noise commands are rewritten to 'xit auto <command>'",
				"Events are logged to ~/.xit/opencode-hooks/events.jsonl",
				"No prompt/response modification",
				"No telemetry",
			}
			plan.Note = "OpenCode plugin: observe mode, fail-open, no blocking by default."
			plan.SafeOption = "xit init opencode --method official_hook --yes"
		} else {
			plan.CanInstall = false
			plan.Note = "OpenCode not detected in PATH. Install OpenCode first."
		}
	}
	return plan
}

func (a *opencodeAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	if plan.SelectedMethod == MethodOfficialHook {
		if !yes {
			return fmt.Errorf("install requires --yes to confirm")
		}
		projectDir, _ := os.Getwd()
		_, err := opencodehook.Install(projectDir, home, false)
		if err != nil {
			return err
		}
		t := cfg.Targets["opencode"]
		t.Enabled = true
		t.Path = plan.Path
		t.Integration = "official_hook"
		cfg.Targets["opencode"] = t
		return config.Save(home, cfg)
	}
	return a.base().Install(home, cfg, plan, yes)
}

func (a *opencodeAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	if !yes {
		return fmt.Errorf("uninstall requires --yes to confirm")
	}
	t, ok := cfg.Targets["opencode"]
	if !ok {
		return fmt.Errorf("unknown target: opencode")
	}
	if t.Integration == "official_hook" {
		projectDir, _ := os.Getwd()
		if err := opencodehook.Uninstall(projectDir); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
	}
	t.Enabled = false
	t.Integration = "manual"
	cfg.Targets["opencode"] = t
	return config.Save(home, cfg)
}

func (a *opencodeAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return a.base().Launch(home, cfg, args, mode)
}
