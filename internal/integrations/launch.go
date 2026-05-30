package integrations

import (
	"fmt"
	"os"

	"github.com/stephenywilson/xit/internal/config"
	"github.com/stephenywilson/xit/internal/session"
)

func launchTarget(home string, cfg *config.Config, target string, args []string, mode string) int {
	t, ok := cfg.Targets[target]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown target: %s\n", target)
		return 1
	}
	if !t.Enabled {
		fmt.Fprintf(os.Stderr, "%s is not initialized. Run: xit init %s\n", target, target)
		return 1
	}
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

	cmdArgs := append([]string{execPath}, args...)
	return session.RunSession(home, cmdArgs, mode, true, false, os.Args[0])
}
