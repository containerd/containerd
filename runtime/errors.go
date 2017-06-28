package runtime

import "errors"

var (
	ErrContainerExists   = errors.New("runtime: container with id already exists")
	ErrContainerNotExist = errors.New("runtime: container does not exist")
	ErrRuntimeNotExist   = errors.New("runtime: runtime does not exist")
	ErrProcessExited     = errors.New("runtime: process already exited")
)
