package plugin

import "errors"

var (
	ErrContainerExists   = errors.New("container with id already exists")
	ErrContainerNotExist = errors.New("container does not exist")
	ErrRuntimeNotExist   = errors.New("runtime does not exist")
)
