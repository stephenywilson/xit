package integrations

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/stephenywilson/xit/internal/config"
)

type Adapter interface {
	Name() string
	DisplayName() string
	Commands() []string
	Detect() TargetStatus
	Doctor(cfg *config.Config) DoctorResult
	PlanInstall(home string, cfg *config.Config, method string) InstallPlan
	Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error
	Uninstall(home string, cfg *config.Config, yes bool) error
	Launch(home string, cfg *config.Config, args []string, mode string) int
}

type baseAdapter struct {
	name        string
	displayName string
	commands    []string
	recommended Method
	supported   []Method
	note        string
}

func (b *baseAdapter) Name() string        { return b.name }
func (b *baseAdapter) DisplayName() string { return b.displayName }
func (b *baseAdapter) Commands() []string  { return b.commands }

func (b *baseAdapter) Detect() TargetStatus {
	path := detectCommand(b.commands)
	return TargetStatus{
		Name:              b.name,
		DisplayName:       b.displayName,
		Detected:          path != "",
		Path:              path,
		RecommendedMethod: b.recommended,
		SupportedMethods:  b.supported,
		Note:              b.note,
	}
}

func (b *baseAdapter) Doctor(cfg *config.Config) DoctorResult {
	status := b.Detect()
	t, _ := cfg.Targets[b.name]

	installed := "no"
	if t.Enabled {
		installed = t.Integration
		if t.ShimEnabled {
			installed = "shim"
		}
	}

	fallback := make([]Method, 0, len(b.supported))
	for _, m := range b.supported {
		if m != b.recommended {
			fallback = append(fallback, m)
		}
	}

	return DoctorResult{
		Name:        b.name,
		Command:     strings.Join(b.commands, ", "),
		Detected:    status.Detected,
		Path:        status.Path,
		Recommended: b.recommended,
		Installed:   installed,
		Fallback:    fallback,
		Note:        b.note,
	}
}

func (b *baseAdapter) PlanInstall(home string, cfg *config.Config, method string) InstallPlan {
	status := b.Detect()
	selected := b.recommended
	if method != "" {
		selected = Method(method)
	}

	actions := []string{}
	canInstall := false
	safeOption := ""
	note := b.note

	switch selected {
	case MethodOfficialHook:
		actions = append(actions,
			"Locate agent settings directory",
			"Add lifecycle/tool hook for Bash tool calls",
			"Hook will rewrite long shell commands through XiT",
			"No prompt/response modification",
			"No telemetry",
		)
		note = "official hook installer: planned, not enabled in this build"
		canInstall = false
		safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", b.name)
	case MethodSessionShim:
		actions = append(actions,
			"Session auto command shims are already available via xit session",
			"Run: xit session "+b.name,
		)
		canInstall = false
		safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", b.name)
	case MethodWrapper:
		actions = append(actions,
			"Enable wrapper in XiT config",
			fmt.Sprintf("Run: xit %s", b.name),
		)
		canInstall = true
		safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", b.name)
	case MethodManual:
		actions = append(actions,
			"No automatic integration",
			"Run commands manually through: xit --mode agent <command>",
		)
		canInstall = false
	}

	// If selected method is not supported, fallback to wrapper if available.
	supported := false
	for _, m := range b.supported {
		if m == selected {
			supported = true
			break
		}
	}
	if !supported {
		for _, m := range b.supported {
			if m == MethodWrapper {
				selected = MethodWrapper
				actions = []string{
					"Selected method not supported for this target",
					"Falling back to wrapper",
					"Enable wrapper in XiT config",
					fmt.Sprintf("Run: xit %s", b.name),
				}
				canInstall = true
				safeOption = fmt.Sprintf("xit init %s --method wrapper --yes", b.name)
				break
			}
		}
	}

	return InstallPlan{
		Target:            b.name,
		Detected:          status.Detected,
		Path:              status.Path,
		RecommendedMethod: b.recommended,
		SelectedMethod:    selected,
		SupportedMethods:  b.supported,
		Actions:           actions,
		SafeOption:        safeOption,
		CanInstall:        canInstall,
		Note:              note,
	}
}

func (b *baseAdapter) Install(home string, cfg *config.Config, plan InstallPlan, yes bool) error {
	if plan.SelectedMethod == MethodWrapper {
		if !yes {
			return fmt.Errorf("install requires --yes to confirm")
		}
		t, ok := cfg.Targets[b.name]
		if !ok {
			return fmt.Errorf("unknown target: %s", b.name)
		}
		path := plan.Path
		if path == "" {
			path = config.DetectPath(b.name)
		}
		if path == "" {
			return fmt.Errorf("%s not found in PATH", b.name)
		}
		t.Enabled = true
		t.Path = path
		t.Integration = "wrapper"
		cfg.Targets[b.name] = t
		return config.Save(home, cfg)
	}
	return fmt.Errorf("method %s is not installable in this build. Use --dry-run to see the plan", plan.SelectedMethod)
}

func (b *baseAdapter) Uninstall(home string, cfg *config.Config, yes bool) error {
	if !yes {
		return fmt.Errorf("uninstall requires --yes to confirm")
	}
	t, ok := cfg.Targets[b.name]
	if !ok {
		return fmt.Errorf("unknown target: %s", b.name)
	}
	t.Enabled = false
	t.Integration = "manual"
	cfg.Targets[b.name] = t
	return config.Save(home, cfg)
}

func (b *baseAdapter) Launch(home string, cfg *config.Config, args []string, mode string) int {
	return launchTarget(home, cfg, b.name, args, mode)
}

var Registry = map[string]Adapter{
	"claude":   &claudeAdapter{},
	"codex":    &codexAdapter{},
	"cursor":   &cursorAdapter{},
	"kimi":     &kimiAdapter{},
	"minimax":  &minimaxAdapter{},
	"opencode": &opencodeAdapter{},
}

func AllAdapters() []Adapter {
	order := []string{"claude", "codex", "cursor", "kimi", "minimax", "opencode"}
	out := make([]Adapter, 0, len(order))
	for _, n := range order {
		if a, ok := Registry[n]; ok {
			out = append(out, a)
		}
	}
	return out
}

func DetectAll(cfg *config.Config) map[string]TargetStatus {
	m := make(map[string]TargetStatus)
	for _, a := range AllAdapters() {
		s := a.Detect()
		m[s.Name] = s
	}
	return m
}

func DoctorAll(cfg *config.Config) []DoctorResult {
	var out []DoctorResult
	for _, a := range AllAdapters() {
		out = append(out, a.Doctor(cfg))
	}
	return out
}

func detectCommand(commands []string) string {
	for _, c := range commands {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}
