//go:build !windows

package camoufoxnative

import "os/exec"

// configureBrowserProcess configures default process attributes for the current platform.
func configureBrowserProcess(command *exec.Cmd, _ bool) {}
