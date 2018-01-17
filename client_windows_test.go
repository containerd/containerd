package containerd

import (
	"os"
	"path/filepath"
)

const (
	defaultAddress = `\\.\pipe\containerd-containerd-test`
	testImage      = "docker.io/microsoft/nanoserver:latest"
)

var (
	defaultRoot  = filepath.Join(os.Getenv("programfiles"), "containerd", "root-test")
	defaultState = filepath.Join(os.Getenv("programfiles"), "containerd", "state-test")
)
