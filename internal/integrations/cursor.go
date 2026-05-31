package integrations

import "github.com/stephenywilson/xit/internal/config"

type cursorAdapter struct{}

func (a *cursorAdapter) base() *baseAdapter {
	return &baseAdapter{
		name:        "cursor",
		displayName: "Cursor",
		commands:    []string{"cursor"},
		recommended: MethodWrapper,
		supported:   []Method{MethodOfficialHook, MethodSessionShim, MethodWrapper, MethodManual},
		note:        "Cursor hook support may depend on installed version / editor context. Use 'xit hook install cursor' for observe mode.",
	}
}

func (a *cursorAdapter) Name() string                { return a.base().Name() }
func (a *cursorAdapter) DisplayName() string         { return a.base().DisplayName() }
func (a *cursorAdapter) Commands() []string          { return a.base().Commands() }
func (a *cursorAdapter) Detect() TargetStatus        { return a.base().Detect() }
func (a *cursorAdapter) Doctor(cfg *config.Config) DoctorResult {
	return a.base().Doctor(cfg)
}
func (a *cursorAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	return a.base().PlanInstall(home, cfg, method)
}
func (a *cursorAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	return a.base().Install(home, cfg, plan, yes)
}
func (a *cursorAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	return a.base().Uninstall(home, cfg, yes)
}
func (a *cursorAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return a.base().Launch(home, cfg, args, mode)
}
