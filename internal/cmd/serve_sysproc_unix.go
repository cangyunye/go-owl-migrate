//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr puts the worker in its own process group (Unix) so it can be
// signalled independently of the serve process.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
