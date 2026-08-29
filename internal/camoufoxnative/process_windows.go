//go:build windows

package camoufoxnative

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureBrowserProcess isolates Camoufox into an independent Windows process group.
func configureBrowserProcess(command *exec.Cmd, headless bool) {
	attributes := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if headless {
		attributes.CreationFlags |= windows.CREATE_NO_WINDOW
		attributes.HideWindow = true
	}
	command.SysProcAttr = attributes
}
