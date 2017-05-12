/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/pkg/truncindex"
	"google.golang.org/grpc"

	"github.com/containerd/containerd"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

const (
	// relativeRootfsPath is the rootfs path relative to bundle path.
	relativeRootfsPath = "rootfs"
	// defaultRuntime is the runtime to use in containerd. We may support
	// other runtime in the future.
	defaultRuntime = "linux"
	// sandboxesDir contains all sandbox root. A sandbox root is the running
	// directory of the sandbox, all files created for the sandbox will be
	// placed under this directory.
	sandboxesDir = "sandboxes"
	// stdinNamedPipe is the name of stdin named pipe.
	stdinNamedPipe = "stdin"
	// stdoutNamedPipe is the name of stdout named pipe.
	stdoutNamedPipe = "stdout"
	// stderrNamedPipe is the name of stderr named pipe.
	stderrNamedPipe = "stderr"
	// Delimiter used to construct container/sandbox names.
	nameDelimiter = "_"
	// netNSFormat is the format of network namespace of a process.
	netNSFormat = "/proc/%v/ns/net"
)

// generateID generates a random unique id.
func generateID() string {
	return stringid.GenerateNonCryptoID()
}

// makeSandboxName generates sandbox name from sandbox metadata. The name
// generated is unique as long as sandbox metadata is unique.
func makeSandboxName(s *runtime.PodSandboxMetadata) string {
	return strings.Join([]string{
		s.Name,      // 0
		s.Namespace, // 1
		s.Uid,       // 2
		fmt.Sprintf("%d", s.Attempt), // 3
	}, nameDelimiter)
}

// getCgroupsPath generates container cgroups path.
func getCgroupsPath(cgroupsParent string, id string) string {
	// TODO(random-liu): [P0] Handle systemd.
	return filepath.Join(cgroupsParent, id)
}

// getSandboxRootDir returns the root directory for managing sandbox files,
// e.g. named pipes.
func getSandboxRootDir(rootDir, id string) string {
	return filepath.Join(rootDir, sandboxesDir, id)
}

// getStreamingPipes returns the stdin/stdout/stderr pipes path in the root.
func getStreamingPipes(rootDir string) (string, string, string) {
	stdin := filepath.Join(rootDir, stdinNamedPipe)
	stdout := filepath.Join(rootDir, stdoutNamedPipe)
	stderr := filepath.Join(rootDir, stderrNamedPipe)
	return stdin, stdout, stderr
}

// getNetworkNamespace returns the network namespace of a process.
func getNetworkNamespace(pid uint32) string {
	return fmt.Sprintf(netNSFormat, pid)
}

// isContainerdContainerNotExistError checks whether a grpc error is containerd
// ErrContainerNotExist error.
// TODO(random-liu): Containerd should expose error better through api.
func isContainerdContainerNotExistError(grpcError error) bool {
	return grpc.ErrorDesc(grpcError) == containerd.ErrContainerNotExist.Error()
}

// getSandbox gets the sandbox metadata from the sandbox store. It returns nil without
// error if the sandbox metadata is not found. It also tries to get full sandbox id and
// retry if the sandbox metadata is not found with the initial id.
func (c *criContainerdService) getSandbox(id string) (*metadata.SandboxMetadata, error) {
	sandbox, err := c.sandboxStore.Get(id)
	if err != nil {
		return nil, fmt.Errorf("sandbox metadata not found: %v", err)
	}
	if sandbox != nil {
		return sandbox, nil
	}
	// sandbox is not found in metadata store, try to extract full id.
	id, err = c.sandboxIDIndex.Get(id)
	if err != nil {
		if err == truncindex.ErrNotExist {
			return nil, nil
		}
		return nil, fmt.Errorf("sandbox id not found: %v", err)
	}
	return c.sandboxStore.Get(id)
}
