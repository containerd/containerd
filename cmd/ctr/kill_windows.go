package main

import "syscall"

func parseSignal(rawSignal string) (syscall.Signal, error) {
	return syscall.SIGKILL, nil
}
