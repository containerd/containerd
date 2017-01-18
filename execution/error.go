package execution

import "fmt"

var (
	ErrContextIsNil      = fmt.Errorf("nil context was passed")
	ErrProcessNotFound   = fmt.Errorf("process not found")
	ErrProcessNotExited  = fmt.Errorf("process has not exited")
	ErrContainerNotFound = fmt.Errorf("container not found")
	ErrContainerExists   = fmt.Errorf("container already exists")
)
