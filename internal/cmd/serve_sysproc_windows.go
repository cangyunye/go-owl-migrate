//go:build windows

package cmd

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr gives the worker its own process group (the Windows
// equivalent of Setpgid) so it can be signalled independently of the serve
// process.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
