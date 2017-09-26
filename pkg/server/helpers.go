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
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/docker/distribution/reference"
	"github.com/docker/docker/pkg/mount"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/opencontainers/selinux/go-selinux/label"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/kubelet/apis/cri/v1alpha1/runtime"

	"github.com/kubernetes-incubator/cri-containerd/pkg/store"
	imagestore "github.com/kubernetes-incubator/cri-containerd/pkg/store/image"
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
	// oomExitReason is the exit reason when process in container is oom killed.
	oomExitReason = "OOMKilled"
)

const (
	// defaultSandboxOOMAdj is default omm adj for sandbox container. (kubernetes#47938).
	defaultSandboxOOMAdj = -998
	// defaultSandboxCPUshares is default cpu shares for sandbox container.
	defaultSandboxCPUshares = 2
	// defaultShmSize is the default size of the sandbox shm.
	defaultShmSize = int64(1024 * 1024 * 64)
	// relativeRootfsPath is the rootfs path relative to bundle path.
	relativeRootfsPath = "rootfs"
	// defaultRuntime is the runtime to use in containerd. We may support
	// other runtime in the future.
	defaultRuntime = "io.containerd.runtime.v1.linux"
	// sandboxesDir contains all sandbox root. A sandbox root is the running
	// directory of the sandbox, all files created for the sandbox will be
	// placed under this directory.
	sandboxesDir = "sandboxes"
	// containersDir contains all container root.
	containersDir = "containers"
	// According to http://man7.org/linux/man-pages/man5/resolv.conf.5.html:
	// "The search list is currently limited to six domains with a total of 256 characters."
	maxDNSSearches = 6
	// Delimiter used to construct container/sandbox names.
	nameDelimiter = "_"
	// netNSFormat is the format of network namespace of a process.
	netNSFormat = "/proc/%v/ns/net"
	// ipcNSFormat is the format of ipc namespace of a process.
	ipcNSFormat = "/proc/%v/ns/ipc"
	// utsNSFormat is the format of uts namespace of a process.
	utsNSFormat = "/proc/%v/ns/uts"
	// devShm is the default path of /dev/shm.
	devShm = "/dev/shm"
	// etcHosts is the default path of /etc/hosts file.
	etcHosts = "/etc/hosts"
	// resolvConfPath is the abs path of resolv.conf on host or container.
	resolvConfPath = "/etc/resolv.conf"
)

const (
	// criContainerdPrefix is common prefix for cri-containerd
	criContainerdPrefix = "io.cri-containerd"
	// containerKindLabel is a label key indicating container is sandbox container or application container
	containerKindLabel = criContainerdPrefix + ".kind"
	// containerKindSandbox is a label value indicating container is sandbox container
	containerKindSandbox = "sandbox"
	// containerKindContainer is a label value indicating container is application container
	containerKindContainer = "container"
	// sandboxMetadataExtension is an extension name that identify metadata of sandbox in CreateContainerRequest
	sandboxMetadataExtension = criContainerdPrefix + ".sandbox.metadata"
	// containerMetadataExtension is an extension name that identify metadata of container in CreateContainerRequest
	containerMetadataExtension = criContainerdPrefix + ".container.metadata"
)

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
func getCgroupsPath(cgroupsParent, id string, systemdCgroup bool) string {
	if systemdCgroup {
		// Convert a.slice/b.slice/c.slice to c.slice.
		p := path.Base(cgroupsParent)
		// runc systemd cgroup path format is "slice:prefix:name".
		return strings.Join([]string{p, "cri-containerd", id}, ":")
	}
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

// getRepoDigestAngTag returns image repoDigest and repoTag of the named image reference.
func getRepoDigestAndTag(namedRef reference.Named, digest imagedigest.Digest, schema1 bool) (string, string) {
	var repoTag, repoDigest string
	if _, ok := namedRef.(reference.NamedTagged); ok {
		repoTag = namedRef.String()
	}
	if _, ok := namedRef.(reference.Canonical); ok {
		repoDigest = namedRef.String()
	} else if !schema1 {
		// digest is not actual repo digest for schema1 image.
		repoDigest = namedRef.Name() + "@" + digest.String()
	}
	return repoDigest, repoTag
}

// localResolve resolves image reference locally and returns corresponding image metadata. It returns
// nil without error if the reference doesn't exist.
func (c *criContainerdService) localResolve(ctx context.Context, ref string) (*imagestore.Image, error) {
	_, err := imagedigest.Parse(ref)
	if err != nil {
		// ref is not image id, try to resolve it locally.
		normalized, err := normalizeImageRef(ref)
		if err != nil {
			return nil, fmt.Errorf("invalid image reference %q: %v", ref, err)
		}
		image, err := c.client.GetImage(ctx, normalized.String())
		if err != nil {
			if errdefs.IsNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("failed to get image from containerd: %v", err)
		}
		desc, err := image.Config(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get image config descriptor: %v", err)
		}
		ref = desc.Digest.String()
	}
	imageID := ref
	image, err := c.imageStore.Get(imageID)
	if err != nil {
		if err == store.ErrNotExist {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get image %q metadata: %v", imageID, err)
	}
	return &image, nil
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
func (c *criContainerdService) ensureImageExists(ctx context.Context, ref string) (*imagestore.Image, error) {
	image, err := c.localResolve(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image %q: %v", ref, err)
	}
	if image != nil {
		return image, nil
	}
	// Pull image to ensure the image exists
	resp, err := c.PullImage(ctx, &runtime.PullImageRequest{Image: &runtime.ImageSpec{Image: ref}})
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %v", ref, err)
	}
	imageID := resp.GetImageRef()
	newImage, err := c.imageStore.Get(imageID)
	if err != nil {
		// It's still possible that someone removed the image right after it is pulled.
		return nil, fmt.Errorf("failed to get image %q metadata after pulling: %v", imageID, err)
	}
	return &newImage, nil
}

// loadCgroup loads the cgroup associated with path if it exists and moves the current process into the cgroup. If the cgroup
// is not created it is created and returned.
func loadCgroup(cgroupPath string) (cgroups.Cgroup, error) {
	cg, err := cgroups.Load(cgroups.V1, cgroups.StaticPath(cgroupPath))
	if err != nil {
		if err != cgroups.ErrCgroupDeleted {
			return nil, err
		}
		if cg, err = cgroups.New(cgroups.V1, cgroups.StaticPath(cgroupPath), &specs.LinuxResources{}); err != nil {
			return nil, err
		}
	}
	if err := cg.Add(cgroups.Process{
		Pid: os.Getpid(),
	}); err != nil {
		return nil, err
	}
	return cg, nil
}

// imageInfo is the information about the image got from containerd.
type imageInfo struct {
	id      string
	chainID imagedigest.Digest
	size    int64
	config  imagespec.ImageConfig
}

// getImageInfo gets image info from containerd.
func getImageInfo(ctx context.Context, image containerd.Image, provider content.Provider) (*imageInfo, error) {
	// Get image information.
	diffIDs, err := image.RootFS(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image diffIDs: %v", err)
	}
	chainID := identity.ChainID(diffIDs)

	size, err := image.Size(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image compressed resource size: %v", err)
	}

	desc, err := image.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image config descriptor: %v", err)
	}
	id := desc.Digest.String()

	rb, err := content.ReadBlob(ctx, provider, desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("failed to read image config from content store: %v", err)
	}
	var ociimage imagespec.Image
	if err := json.Unmarshal(rb, &ociimage); err != nil {
		return nil, fmt.Errorf("failed to unmarshal image config %s: %v", rb, err)
	}

	return &imageInfo{
		id:      id,
		chainID: chainID,
		size:    size,
		config:  ociimage.Config,
	}, nil
}

func initSelinuxOpts(selinuxOpt *runtime.SELinuxOption) (string, string, error) {
	if selinuxOpt == nil {
		return "", "", nil
	}

	// Should ignored selinuxOpts if they are incomplete.
	if selinuxOpt.GetUser() == "" ||
		selinuxOpt.GetRole() == "" ||
		selinuxOpt.GetType() == "" ||
		selinuxOpt.GetLevel() == "" {
		return "", "", nil
	}

	labelOpts := fmt.Sprintf("%s:%s:%s:%s",
		selinuxOpt.GetUser(),
		selinuxOpt.GetRole(),
		selinuxOpt.GetType(),
		selinuxOpt.GetLevel())
	return label.InitLabels(selinux.DupSecOpt(labelOpts))
}

// isInCRIMounts checks whether a destination is in CRI mount list.
func isInCRIMounts(dst string, mounts []*runtime.Mount) bool {
	for _, m := range mounts {
		if m.ContainerPath == dst {
			return true
		}
	}
	return false
}

//TODO: Replace with `mount.Lookup()`in containerd after #257 is marged
func getMountInfo(mountInfos []*mount.Info, dir string) *mount.Info {
	for _, m := range mountInfos {
		if m.Mountpoint == dir {
			return m
		}
	}
	return nil
}

func getSourceMount(source string, mountInfos []*mount.Info) (string, string, error) {
	mountinfo := getMountInfo(mountInfos, source)
	if mountinfo != nil {
		return source, mountinfo.Optional, nil
	}

	path := source
	for {
		path = filepath.Dir(path)
		mountinfo = getMountInfo(mountInfos, path)
		if mountinfo != nil {
			return path, mountinfo.Optional, nil
		}

		if path == "/" {
			break
		}
	}

	// If we are here, we did not find parent mount. Something is wrong.
	return "", "", fmt.Errorf("Could not find source mount of %s", source)
}

// filterLabel returns a label filter. Use `%q` here because containerd
// filter needs extra quote to work properly.
func filterLabel(k, v string) string {
	return fmt.Sprintf("labels.%q==%q", k, v)
}
