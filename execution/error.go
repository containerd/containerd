package execution

import "errors"

var (
	ErrProcessNotFound   = errors.New("process not found")
	ErrProcessNotExited  = errors.New("process has not exited")
	ErrContainerNotFound = errors.New("container not found")
	ErrContainerExists   = errors.New("container already exists")
)
