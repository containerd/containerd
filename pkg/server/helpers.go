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
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	containerdmetadata "github.com/containerd/containerd/metadata"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/pkg/truncindex"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/metadata"
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
	// unknownExitCode is the exit code when exit reason is unknown.
	unknownExitCode = 255
)

const (
	// defaultSandboxImage is the image used by sandbox container.
	defaultSandboxImage = "gcr.io/google_containers/pause:3.0"
	// defaultShmSize is the default size of the sandbox shm.
	defaultShmSize = int64(1024 * 1024 * 64)
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
	// According to http://man7.org/linux/man-pages/man5/resolv.conf.5.html:
	// "The search list is currently limited to six domains with a total of 256 characters."
	maxDNSSearches = 6
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
	// devShm is the default path of /dev/shm.
	devShm = "/dev/shm"
	// etcHosts is the default path of /etc/hosts file.
	etcHosts = "/etc/hosts"
	// resolvConfPath is the abs path of resolv.conf on host or container.
	resolvConfPath = "/etc/resolv.conf"
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

// getStreamingPipes returns the stdin/stdout/stderr pipes path in the
// container/sandbox root.
func getStreamingPipes(rootDir string) (string, string, string) {
	stdin := filepath.Join(rootDir, stdinNamedPipe)
	stdout := filepath.Join(rootDir, stdoutNamedPipe)
	stderr := filepath.Join(rootDir, stderrNamedPipe)
	return stdin, stdout, stderr
}

// getSandboxHosts returns the hosts file path inside the sandbox root directory.
func getSandboxHosts(sandboxRootDir string) string {
	return filepath.Join(sandboxRootDir, "hosts")
}

// getResolvPath returns resolv.conf filepath for specified sandbox.
func getResolvPath(sandboxRoot string) string {
	return filepath.Join(sandboxRoot, "resolv.conf")
}

// getSandboxDevShm returns the shm file path inside the sandbox root directory.
func getSandboxDevShm(sandboxRootDir string) string {
	return filepath.Join(sandboxRootDir, "shm")
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

// isContainerdGRPCNotFoundError checks whether a grpc error is not found error.
func isContainerdGRPCNotFoundError(grpcError error) bool {
	return grpc.Code(grpcError) == codes.NotFound
}

// isRuncProcessAlreadyFinishedError checks whether a grpc error is a process already
// finished error.
// TODO(random-liu): Containerd should expose this error in api. (containerd#999)
func isRuncProcessAlreadyFinishedError(grpcError error) bool {
	return strings.Contains(grpc.ErrorDesc(grpcError), "os: process already finished")
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

// normalizeImageRef normalizes the image reference following the docker convention. This is added
// mainly for backward compatibility.
// The reference returned can only be either tagged or digested. For reference contains both tag
// and digest, the function returns digested reference, e.g. docker.io/library/busybox:latest@
// sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa will be returned as
// docker.io/library/busybox@sha256:7cc4b5aefd1d0cadf8d97d4350462ba51c694ebca145b08d7d41b41acc8db5aa.
func normalizeImageRef(ref string) (reference.Named, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return nil, err
	}
	if _, ok := named.(reference.NamedTagged); ok {
		if canonical, ok := named.(reference.Canonical); ok {
			// The reference is both tagged and digested, only
			// return digested.
			newNamed, err := reference.WithName(canonical.Name())
			if err != nil {
				return nil, err
			}
			newCanonical, err := reference.WithDigest(newNamed, canonical.Digest())
			if err != nil {
				return nil, err
			}
			return newCanonical, nil
		}
	}
	return reference.TagNameOnly(named), nil
}

// getImageInfo returns image chainID, compressed size and oci config. Note that getImageInfo
// assumes that the image has been pulled or it will return an error.
func (c *criContainerdService) getImageInfo(ctx context.Context, ref string) (
	imagedigest.Digest, int64, *imagespec.ImageConfig, error) {
	normalized, err := normalizeImageRef(ref)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to normalize image reference %q: %v", ref, err)
	}
	normalizedRef := normalized.String()
	image, err := c.imageStoreService.Get(ctx, normalizedRef)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get image %q from containerd image store: %v",
			normalizedRef, err)
	}
	// Get image config
	desc, err := image.Config(ctx, c.contentStoreService)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get image config descriptor: %v", err)
	}
	rc, err := c.contentStoreService.Reader(ctx, desc.Digest)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get image config reader: %v", err)
	}
	defer rc.Close()
	var imageConfig imagespec.Image
	if err = json.NewDecoder(rc).Decode(&imageConfig); err != nil {
		return "", 0, nil, fmt.Errorf("failed to decode image config: %v", err)
	}
	// Get image chainID
	diffIDs, err := image.RootFS(ctx, c.contentStoreService)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get image diff ids: %v", err)
	}
	chainID := identity.ChainID(diffIDs)
	// Get image size
	size, err := image.Size(ctx, c.contentStoreService)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to get image size: %v", err)
	}
	return chainID, size, &imageConfig.Config, nil
}

// getRepoDigestAngTag returns image repoDigest and repoTag of the named image reference.
func getRepoDigestAndTag(namedRef reference.Named, digest imagedigest.Digest) (string, string) {
	var repoTag string
	if _, ok := namedRef.(reference.NamedTagged); ok {
		repoTag = namedRef.String()
	}
	repoDigest := namedRef.Name() + "@" + digest.String()
	return repoDigest, repoTag
}

// localResolve resolves image reference locally and returns corresponding image metadata. It returns
// nil without error if the reference doesn't exist.
func (c *criContainerdService) localResolve(ctx context.Context, ref string) (*metadata.ImageMetadata, error) {
	_, err := imagedigest.Parse(ref)
	if err != nil {
		// ref is not image id, try to resolve it locally.
		normalized, err := normalizeImageRef(ref)
		if err != nil {
			return nil, fmt.Errorf("invalid image reference %q: %v", ref, err)
		}
		image, err := c.imageStoreService.Get(ctx, normalized.String())
		if err != nil {
			if containerdmetadata.IsNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("an error occurred when getting image %q from containerd image store: %v",
				normalized.String(), err)
		}
		desc, err := image.Config(ctx, c.contentStoreService)
		if err != nil {
			return nil, fmt.Errorf("failed to get image config descriptor: %v", err)
		}
		ref = desc.Digest.String()
	}
	imageID := ref
	meta, err := c.imageMetadataStore.Get(imageID)
	if err != nil {
		if metadata.IsNotExistError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get image %q metadata: %v", imageID, err)
	}
	return meta, nil
}

// getUserFromImage gets uid or user name of the image user.
// If user is numeric, it will be treated as uid; or else, it is treated as user name.
func getUserFromImage(user string) (*int64, string) {
	// return both empty if user is not specified in the image.
	if user == "" {
		return nil, ""
	}
	// split instances where the id may contain user:group
	user = strings.Split(user, ":")[0]
	// user could be either uid or user name. Try to interpret as numeric uid.
	uid, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		// If user is non numeric, assume it's user name.
		return nil, user
	}
	// If user is a numeric uid.
	return &uid, ""
}

// ensureImageExists returns corresponding metadata of the image reference, if image is not
// pulled yet, the function will pull the image.
func (c *criContainerdService) ensureImageExists(ctx context.Context, ref string) (*metadata.ImageMetadata, error) {
	meta, err := c.localResolve(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image %q: %v", ref, err)
	}
	if meta != nil {
		return meta, nil
	}
	// Pull image to ensure the image exists
	resp, err := c.PullImage(ctx, &runtime.PullImageRequest{Image: &runtime.ImageSpec{Image: ref}})
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %v", ref, err)
	}
	imageID := resp.GetImageRef()
	meta, err = c.imageMetadataStore.Get(imageID)
	if err != nil {
		// It's still possible that someone removed the image right after it is pulled.
		return nil, fmt.Errorf("failed to get image %q metadata after pulling: %v", imageID, err)
	}
	return meta, nil
}
