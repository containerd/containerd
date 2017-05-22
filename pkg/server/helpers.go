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
	"io"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/pkg/truncindex"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/containerd/containerd"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"

	"k8s.io/kubernetes/pkg/kubelet/api/v1alpha1/runtime"
)

const (
	// errorStartReason is the exit reason when fails to start container.
	errorStartReason = "StartError"
	// errorStartExitCode is the exit code when fails to start container.
	// 128 is the same with Docker's behavior.
	errorStartExitCode = 128
	// completeExitReason is the exit reason when container exits with code 0.
	completeExitReason = "Completed"
	// errorExitReason is the exit reason when container exits with code non-zero.
	errorExitReason = "Error"
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
	// containersDir contains all container root.
	containersDir = "containers"
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
	// ipcNSFormat is the format of ipc namespace of a process.
	ipcNSFormat = "/proc/%v/ns/ipc"
	// utsNSFormat is the format of uts namespace of a process.
	utsNSFormat = "/proc/%v/ns/uts"
	// pidNSFormat is the format of pid namespace of a process.
	pidNSFormat = "/proc/%v/ns/pid"
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

// makeContainerName generates container name from sandbox and container metadata.
// The name generated is unique as long as the sandbox container combination is
// unique.
func makeContainerName(c *runtime.ContainerMetadata, s *runtime.PodSandboxMetadata) string {
	return strings.Join([]string{
		c.Name,      // 0
		s.Name,      // 1: sandbox name
		s.Namespace, // 2: sandbox namespace
		s.Uid,       // 3: sandbox uid
		fmt.Sprintf("%d", c.Attempt), // 4
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

// getContainerRootDir returns the root directory for managing container files.
func getContainerRootDir(rootDir, id string) string {
	return filepath.Join(rootDir, containersDir, id)
}

// getStreamingPipes returns the stdin/stdout/stderr pipes path in the root.
func getStreamingPipes(rootDir string) (string, string, string) {
	stdin := filepath.Join(rootDir, stdinNamedPipe)
	stdout := filepath.Join(rootDir, stdoutNamedPipe)
	stderr := filepath.Join(rootDir, stderrNamedPipe)
	return stdin, stdout, stderr
}

// prepareStreamingPipes prepares stream named pipe for container. returns nil
// streaming handler if corresponding stream path is empty.
func (c *criContainerdService) prepareStreamingPipes(ctx context.Context, stdin, stdout, stderr string) (
	i io.WriteCloser, o io.ReadCloser, e io.ReadCloser, retErr error) {
	pipes := map[string]io.ReadWriteCloser{}
	for t, stream := range map[string]struct {
		path string
		flag int
	}{
		"stdin":  {stdin, syscall.O_WRONLY | syscall.O_CREAT | syscall.O_NONBLOCK},
		"stdout": {stdout, syscall.O_RDONLY | syscall.O_CREAT | syscall.O_NONBLOCK},
		"stderr": {stderr, syscall.O_RDONLY | syscall.O_CREAT | syscall.O_NONBLOCK},
	} {
		if stream.path == "" {
			continue
		}
		s, err := c.os.OpenFifo(ctx, stream.path, stream.flag, 0700)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to open named pipe %q: %v",
				stream.path, err)
		}
		defer func(cl io.Closer) {
			if retErr != nil {
				cl.Close()
			}
		}(s)
		pipes[t] = s
	}
	return pipes["stdin"], pipes["stdout"], pipes["stderr"], nil
}

// getNetworkNamespace returns the network namespace of a process.
func getNetworkNamespace(pid uint32) string {
	return fmt.Sprintf(netNSFormat, pid)
}

// getIPCNamespace returns the ipc namespace of a process.
func getIPCNamespace(pid uint32) string {
	return fmt.Sprintf(ipcNSFormat, pid)
}

// getUTSNamespace returns the uts namespace of a process.
func getUTSNamespace(pid uint32) string {
	return fmt.Sprintf(utsNSFormat, pid)
}

// getPIDNamespace returns the pid namespace of a process.
func getPIDNamespace(pid uint32) string {
	return fmt.Sprintf(pidNSFormat, pid)
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
	if err != nil && !metadata.IsNotExistError(err) {
		return nil, fmt.Errorf("sandbox metadata not found: %v", err)
	}
	if err == nil {
		return sandbox, nil
	}
	// sandbox is not found in metadata store, try to extract full id.
	id, indexErr := c.sandboxIDIndex.Get(id)
	if indexErr != nil {
		if indexErr == truncindex.ErrNotExist {
			// Return the original error if sandbox id is not found.
			return nil, err
		}
		return nil, fmt.Errorf("sandbox id not found: %v", err)
	}
	return c.sandboxStore.Get(id)
}

// criContainerStateToString formats CRI container state to string.
func criContainerStateToString(state runtime.ContainerState) string {
	return runtime.ContainerState_name[int32(state)]
}
