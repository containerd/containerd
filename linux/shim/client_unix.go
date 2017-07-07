// +build !linux,!windows

package shim

import "syscall"

var atter = syscall.SysProcAttr{
	Setpgid: true,
}
