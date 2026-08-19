//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

// ConfigureDetached configures cmd to run in a detached process group on Windows.
func ConfigureDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008,
	}
}
