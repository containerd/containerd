// +build linux

package shim

import "syscall"

var atter = syscall.SysProcAttr{
	Cloneflags: syscall.CLONE_NEWNS,
	Setpgid:    true,
}
