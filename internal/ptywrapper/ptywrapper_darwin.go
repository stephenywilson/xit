//go:build darwin

package ptywrapper

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	iocPttyGrant = syscall.TIOCPTYGRANT
	iocPttyUnlk  = syscall.TIOCPTYUNLK
	iocPttyGname = syscall.TIOCPTYGNAME
)

// Start runs cmd inside a PTY, copying I/O between the terminal and the caller's stdin/stdout.
// If log is non-nil, all output is also written to log.
// It returns the command's exit code.
func Start(cmd *exec.Cmd, log io.Writer) (int, error) {
	master, slaveName, err := openpty()
	if err != nil {
		// Fallback when PTY is unavailable (sandbox, Docker, CI, etc.)
		return startFallback(cmd, log)
	}

	slave, err := syscall.Open(slaveName, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		syscall.Close(master)
		return startFallback(cmd, log)
	}

	// Dup slave fd so stdin/stdout/stderr each own their own descriptor.
	slaveOut, err := syscall.Dup(slave)
	if err != nil {
		syscall.Close(master)
		syscall.Close(slave)
		return startFallback(cmd, log)
	}
	slaveErr, err := syscall.Dup(slave)
	if err != nil {
		syscall.Close(master)
		syscall.Close(slave)
		syscall.Close(slaveOut)
		return startFallback(cmd, log)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	cmd.Stdin = os.NewFile(uintptr(slave), "slave")
	cmd.Stdout = os.NewFile(uintptr(slaveOut), "slave")
	cmd.Stderr = os.NewFile(uintptr(slaveErr), "slave")

	if err := cmd.Start(); err != nil {
		syscall.Close(master)
		return 1, err
	}

	masterFile := os.NewFile(uintptr(master), "master")

	outWriter := io.Writer(os.Stdout)
	if log != nil {
		outWriter = io.MultiWriter(os.Stdout, log)
	}
	go func() { io.Copy(outWriter, masterFile) }()
	go func() { io.Copy(masterFile, os.Stdin) }()

	err = cmd.Wait()
	masterFile.Close() // unblock PTY->stdout copy

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func startFallback(cmd *exec.Cmd, log io.Writer) (int, error) {
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

func openpty() (master int, slaveName string, err error) {
	master, err = syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return 0, "", err
	}

	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(master), uintptr(iocPttyGrant), 0); errno != 0 {
		syscall.Close(master)
		return 0, "", errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(master), uintptr(iocPttyUnlk), 0); errno != 0 {
		syscall.Close(master)
		return 0, "", errno
	}

	var buf [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(master), uintptr(iocPttyGname), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		syscall.Close(master)
		return 0, "", errno
	}

	slaveName = cstring(buf[:])
	return master, slaveName, nil
}

func cstring(b []byte) string {
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
