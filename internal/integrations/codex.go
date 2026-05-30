package integrations

import "github.com/stephenywilson/xit/internal/config"

type codexAdapter struct{}

func (a *codexAdapter) base() *baseAdapter {
	return &baseAdapter{
		name:        "codex",
		displayName: "Codex",
		commands:    []string{"codex"},
		recommended: MethodOfficialHook,
		supported:   []Method{MethodOfficialHook, MethodSessionShim, MethodWrapper},
		note:        "Codex supports hooks; official hook should be preferred.",
	}
}

func (a *codexAdapter) Name() string                { return a.base().Name() }
func (a *codexAdapter) DisplayName() string         { return a.base().DisplayName() }
func (a *codexAdapter) Commands() []string          { return a.base().Commands() }
func (a *codexAdapter) Detect() TargetStatus        { return a.base().Detect() }
func (a *codexAdapter) Doctor(cfg *config.Config) DoctorResult {
	return a.base().Doctor(cfg)
}
func (a *codexAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	return a.base().PlanInstall(home, cfg, method)
}
func (a *codexAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	return a.base().Install(home, cfg, plan, yes)
}
func (a *codexAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	return a.base().Uninstall(home, cfg, yes)
}
func (a *codexAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return a.base().Launch(home, cfg, args, mode)
}
