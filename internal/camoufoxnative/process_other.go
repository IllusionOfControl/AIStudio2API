//go:build !windows

package camoufoxnative

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureBrowserProcess isolates Camoufox into an independent process group.
func configureBrowserProcess(command *exec.Cmd, _ bool) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// attachBrowserProcess relies on process group reclamation on non-Windows platforms.
func attachBrowserProcess(*exec.Cmd) error {
	return nil
}

// terminateBrowserProcess terminates the Camoufox process group.
func terminateBrowserProcess(ctx context.Context, command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
