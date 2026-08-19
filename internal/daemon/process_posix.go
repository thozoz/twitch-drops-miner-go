//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// ConfigureDetached configures cmd to run in a separate process group on POSIX systems.
func ConfigureDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}
