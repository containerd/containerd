// +build !linux,!windows

package shim

import (
	"os/exec"
	"syscall"
)

var atter = syscall.SysProcAttr{
	Setpgid: true,
}

func setCgroup(cgroupPath string, cmd *exec.Cmd) error {
	return nil
}
