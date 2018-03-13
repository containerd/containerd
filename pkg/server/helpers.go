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
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/docker/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/opencontainers/selinux/go-selinux/label"
	"golang.org/x/net/context"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"
	"k8s.io/kubernetes/pkg/util/sysctl"

	"github.com/containerd/cri/pkg/store"
	imagestore "github.com/containerd/cri/pkg/store/image"
	"github.com/containerd/cri/pkg/util"
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
	// pidNSFormat is the format of pid namespace of a process.
	pidNSFormat = "/proc/%v/ns/pid"
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

// getPIDNamespace returns the pid namespace of a process.
func getPIDNamespace(pid uint32) string {
	return fmt.Sprintf(pidNSFormat, pid)
}

// criContainerStateToString formats CRI container state to string.
func criContainerStateToString(state runtime.ContainerState) string {
	return runtime.ContainerState_name[int32(state)]
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
func (c *criContainerdService) localResolve(ctx context.Context, refOrID string) (*imagestore.Image, error) {
	getImageID := func(refOrId string) string {
		if _, err := imagedigest.Parse(refOrID); err == nil {
			return refOrID
		}
		return func(ref string) string {
			// ref is not image id, try to resolve it locally.
			normalized, err := util.NormalizeImageRef(ref)
			if err != nil {
				return ""
			}
			image, err := c.client.GetImage(ctx, normalized.String())
			if err != nil {
				return ""
			}
			desc, err := image.Config(ctx)
			if err != nil {
				return ""
			}
			return desc.Digest.String()
		}(refOrID)
	}

	imageID := getImageID(refOrID)
	if imageID == "" {
		// Try to treat ref as imageID
		imageID = refOrID
	}
	image, err := c.imageStore.Get(imageID)
	if err != nil {
		if err == store.ErrNotExist {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get image %q : %v", imageID, err)
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

// imageInfo is the information about the image got from containerd.
type imageInfo struct {
	id        string
	chainID   imagedigest.Digest
	size      int64
	imagespec imagespec.Image
}

// getImageInfo gets image info from containerd.
func getImageInfo(ctx context.Context, image containerd.Image) (*imageInfo, error) {
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

	rb, err := content.ReadBlob(ctx, image.ContentStore(), desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("failed to read image config from content store: %v", err)
	}
	var ociimage imagespec.Image
	if err := json.Unmarshal(rb, &ociimage); err != nil {
		return nil, fmt.Errorf("failed to unmarshal image config %s: %v", rb, err)
	}

	return &imageInfo{
		id:        id,
		chainID:   chainID,
		size:      size,
		imagespec: ociimage,
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

// filterLabel returns a label filter. Use `%q` here because containerd
// filter needs extra quote to work properly.
func filterLabel(k, v string) string {
	return fmt.Sprintf("labels.%q==%q", k, v)
}

// buildLabel builds the labels from config to be passed to containerd
func buildLabels(configLabels map[string]string, containerType string) map[string]string {
	labels := make(map[string]string)
	for k, v := range configLabels {
		labels[k] = v
	}
	labels[containerKindLabel] = containerType
	return labels
}

// newSpecGenerator creates a new spec generator for the runtime spec.
func newSpecGenerator(spec *runtimespec.Spec) generate.Generator {
	g := generate.NewFromSpec(spec)
	g.HostSpecific = true
	return g
}

// disableNetNSDAD disables duplicate address detection in the network namespace.
// DAD has a negative affect on sandbox start latency, since we have to wait
// a second or more for the addresses to leave the "tentative" state.
func disableNetNSDAD(ns string) error {
	dad := "net/ipv6/conf/default/accept_dad"

	sysctlBin, err := exec.LookPath("sysctl")
	if err != nil {
		return fmt.Errorf("could not find sysctl binary: %v", err)
	}

	nsenterBin, err := exec.LookPath("nsenter")
	if err != nil {
		return fmt.Errorf("could not find nsenter binary: %v", err)
	}

	// If the sysctl doesn't exist, it means ipv6 is disabled.
	if _, err := sysctl.New().GetSysctl(dad); err != nil {
		return nil
	}

	output, err := exec.Command(nsenterBin,
		fmt.Sprintf("--net=%s", ns), "-F", "--",
		sysctlBin, "-w", fmt.Sprintf("%s=%s", dad, "0"),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to write sysctl %q - output: %s, error: %s",
			dad, output, err)
	}
	return nil
}
