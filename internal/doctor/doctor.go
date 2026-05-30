package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/stephenywilson/xit/internal/config"
)

type Report struct {
	Version    string
	XitPath    string
	Shell      string
	OSArch     string
	Home       string
	XiTHome    string
	ConfigPath string
	ConfigOK   bool
	Targets    map[string]string
	GoPath     string
}

func Run(xitHome string) *Report {
	r := &Report{
		Version:    os.Getenv("XIT_VERSION"),
		Shell:      os.Getenv("SHELL"),
		OSArch:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		Home:       os.Getenv("HOME"),
		XiTHome:    xitHome,
		ConfigPath: config.Path(xitHome),
		Targets:    make(map[string]string),
	}

	if r.Shell == "" {
		r.Shell = "unknown"
	}

	if p, err := exec.LookPath("xit"); err == nil {
		r.XitPath = p
	} else {
		r.XitPath = "not in PATH"
	}

	if _, err := os.Stat(r.ConfigPath); err == nil {
		r.ConfigOK = true
	}

	for _, name := range []string{"kimi", "claude", "codex", "gemini", "cursor"} {
		if p, err := exec.LookPath(name); err == nil {
			r.Targets[name] = p
		} else {
			r.Targets[name] = ""
		}
	}

	if p, err := exec.LookPath("go"); err == nil {
		r.GoPath = p
	}

	return r
}

func (r *Report) String() string {
	var b strings.Builder
	b.WriteString("XiT Doctor\n\n")
	b.WriteString(fmt.Sprintf("version:      %s\n", r.Version))
	b.WriteString(fmt.Sprintf("xit path:     %s\n", r.XitPath))
	b.WriteString(fmt.Sprintf("shell:        %s\n", r.Shell))
	b.WriteString(fmt.Sprintf("os/arch:      %s\n", r.OSArch))
	b.WriteString(fmt.Sprintf("xit home:     %s\n", r.XiTHome))
	if r.ConfigOK {
		b.WriteString(fmt.Sprintf("config:       %s\n", r.ConfigPath))
	} else {
		b.WriteString("config:       missing\n")
	}
	b.WriteString("\nAI CLI:\n\n")
	for _, name := range []string{"kimi", "claude", "codex", "gemini", "cursor"} {
		if p := r.Targets[name]; p != "" {
			b.WriteString(fmt.Sprintf("* %s: found %s\n", name, p))
		} else {
			b.WriteString(fmt.Sprintf("* %s: not found\n", name))
		}
	}
	b.WriteString("\nCapabilities:\n\n")
	b.WriteString("* manual compression: ready\n")
	b.WriteString("* session mode:       ready\n")
	b.WriteString("* wrapper mode:       available\n")
	b.WriteString("* auto hook:          not installed\n")
	if !r.ConfigOK {
		b.WriteString("\nRecommendation:\n")
		b.WriteString("Run: xit init\n")
	}
	return b.String()
}
