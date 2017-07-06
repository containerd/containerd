// +build !linux,!windows

package shim

import (
	"context"
	"os/exec"
	"syscall"
)

var atter = syscall.SysProcAttr{
	Setpgid: true,
}

func setCgroup(ctx context.Context, config Config, cmd *exec.Cmd) error {
	return nil
}
