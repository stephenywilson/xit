//go:build !darwin

package ptywrapper

import (
	"io"
	"os"
	"os/exec"
)

// Start runs cmd with stdio passthrough. On non-Darwin platforms PTY is not yet
// supported, so this uses direct stdin/stdout/stderr forwarding.
// If log is non-nil, all output is also written to log.
// It returns the command's exit code.
func Start(cmd *exec.Cmd, log io.Writer) (int, error) {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	if log != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, log)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
