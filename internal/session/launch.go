package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/stephenywilson/xit/internal/autoshim"
	"github.com/stephenywilson/xit/internal/ptywrapper"
	"github.com/stephenywilson/xit/internal/runner"
)

// RunSession executes a command inside a XiT session with optional auto-shims and banners.
func RunSession(home string, cmdArgs []string, mode string, autoShims, quiet bool, xitBin string) int {
	if len(cmdArgs) < 1 {
		fmt.Fprintln(os.Stderr, "usage: xit session [--quiet] [--mode <mode>] [--no-auto-shims] <command...>")
		return 1
	}

	if _, err := exec.LookPath(cmdArgs[0]); err != nil {
		fmt.Fprintf(os.Stderr, "xit: command not found: %s\n", cmdArgs[0])
		return 127
	}

	xh := &runner.XitHome{Path: home}
	if err := xh.Ensure(); err != nil {
		fmt.Fprintln(os.Stderr, "xit: cannot create home:", err)
		return 1
	}

	sess, err := New(home, cmdArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: cannot create session:", err)
		return 1
	}

	logFile, err := os.OpenFile(filepath.Join(sess.Dir, "raw.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: cannot open session raw log:", err)
		return 1
	}
	defer logFile.Close()

	if !quiet {
		fmt.Fprint(logFile, sess.StartBanner(mode))
		fmt.Print(sess.StartBanner(mode))
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(os.Environ(), sess.Environ()...)

	if autoShims {
		self, _ := exec.LookPath(xitBin)
		if self == "" {
			self = xitBin
		}
		shimDir, shimEnv, err := autoshim.CreateShims(sess.Dir, self, autoshim.DefaultTools)
		if err == nil {
			cmd.Env = append(cmd.Env, shimEnv...)
			for i, e := range cmd.Env {
				if strings.HasPrefix(e, "PATH=") {
					cmd.Env[i] = "PATH=" + shimDir + string(os.PathListSeparator) + e[len("PATH="):]
					break
				}
			}
		}
	}

	exitCode, err := ptywrapper.Start(cmd, logFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "xit: session error:", err)
		return 1
	}

	if !quiet {
		report := sess.EndReport(mode, exitCode)
		fmt.Fprint(logFile, report)
		fmt.Print(report)
	}

	return exitCode
}
