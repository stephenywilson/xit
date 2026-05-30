package integrations

import "github.com/stephenywilson/xit/internal/config"

type minimaxAdapter struct{}

func (a *minimaxAdapter) base() *baseAdapter {
	return &baseAdapter{
		name:        "minimax",
		displayName: "MiniMax",
		commands:    []string{"minimax", "mmx"},
		recommended: MethodWrapper,
		supported:   []Method{MethodSessionShim, MethodWrapper, MethodManual},
		note:        "MiniMax CLI may be more of an agent tool CLI than a coding agent CLI; do not overclaim official hook support.",
	}
}

func (a *minimaxAdapter) Name() string                { return a.base().Name() }
func (a *minimaxAdapter) DisplayName() string         { return a.base().DisplayName() }
func (a *minimaxAdapter) Commands() []string          { return a.base().Commands() }
func (a *minimaxAdapter) Detect() TargetStatus        { return a.base().Detect() }
func (a *minimaxAdapter) Doctor(cfg *config.Config) DoctorResult {
	return a.base().Doctor(cfg)
}
func (a *minimaxAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	return a.base().PlanInstall(home, cfg, method)
}
func (a *minimaxAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	return a.base().Install(home, cfg, plan, yes)
}
func (a *minimaxAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	return a.base().Uninstall(home, cfg, yes)
}
func (a *minimaxAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return a.base().Launch(home, cfg, args, mode)
}
