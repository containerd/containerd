// +build !windows

package osutils

import "syscall"

// Exit is the wait4 information from an exited process
type Exit struct {
	Pid    int
	Status int
}

// Reap reaps all child processes for the calling process and returns their
// exit information
func Reap(wait bool) (exits []Exit, rus syscall.Rusage, err error) {
	var (
		ws  syscall.WaitStatus
	)
	flag := syscall.WNOHANG
	if wait {
		flag = 0
	}
	for {
		pid, err := syscall.Wait4(-1, &ws, flag, &rus)
		if err != nil {
			if err == syscall.ECHILD {
				return exits, rus, nil
			}
			return exits, rus, err
		}
		if pid <= 0 {
			return exits, rus, nil
		}
		exits = append(exits, Exit{
			Pid:    pid,
			Status: exitStatus(ws),
		})
	}
}

const exitSignalOffset = 128

// exitStatus returns the correct exit status for a process based on if it
// was signaled or exited cleanly
func exitStatus(status syscall.WaitStatus) int {
	if status.Signaled() {
		return exitSignalOffset + int(status.Signal())
	}
	return status.ExitStatus()
}
