// +build libcontainer

package supervisor

import (
	"github.com/docker/containerd/runtime"
)

func newRuntime(stateDir string) (runtime.Runtime, error) {
	return runtime.NewRuntime(stateDir)
}
