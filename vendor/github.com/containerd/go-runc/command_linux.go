package runc

import (
	"context"
	"os/exec"
	"syscall"
)

func (r *Runc) command(context context.Context, args ...string) *exec.Cmd {
	command := r.Command
	if command == "" {
		command = DefaultCommand
	}
	cmd := exec.CommandContext(context, command, append(r.args(), args...)...)
	if r.PdeathSignal != 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: r.PdeathSignal,
		}
	}
	return cmd
}
