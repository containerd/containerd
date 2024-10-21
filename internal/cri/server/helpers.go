/*
   Copyright The containerd Authors.

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
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/typeurl/v2"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	imagestore "github.com/containerd/containerd/v2/internal/cri/store/image"
	"github.com/containerd/errdefs"
	"github.com/containerd/log"
)

// TODO: Move common helpers for sbserver and podsandbox to a dedicated package once basic services are functinal.

const (
	// errorStartReason is the exit reason when fails to start container.
	errorStartReason = "StartError"
	// errorStartExitCode is the exit code when fails to start container.
	// 128 is the same with Docker's behavior.
	// TODO(windows): Figure out what should be used for windows.
	errorStartExitCode = 128
	// completeExitReason is the exit reason when container exits with code 0.
	completeExitReason = "Completed"
	// errorExitReason is the exit reason when container exits with code non-zero.
	errorExitReason = "Error"
	// oomExitReason is the exit reason when process in container is oom killed.
	oomExitReason = "OOMKilled"

	// sandboxesDir contains all sandbox root. A sandbox root is the running
	// directory of the sandbox, all files created for the sandbox will be
	// placed under this directory.
	sandboxesDir = "sandboxes"
	// containersDir contains all container root.
	containersDir = "containers"
	// imageVolumeDir contains all image volume root.
	imageVolumeDir = "image-volumes"
	// Delimiter used to construct container/sandbox names.
	nameDelimiter = "_"

	// defaultIfName is the default network interface for the pods
	defaultIfName = "eth0"

	// devShm is the default path of /dev/shm.
	devShm = "/dev/shm"
	// etcHosts is the default path of /etc/hosts file.
	etcHosts = "/etc/hosts"
	// etcHostname is the default path of /etc/hostname file.
	etcHostname = "/etc/hostname"
	// resolvConfPath is the abs path of resolv.conf on host or container.
	resolvConfPath = "/etc/resolv.conf"
)

// getSandboxRootDir returns the root directory for managing sandbox files,
// e.g. hosts files.
func (c *criService) getSandboxRootDir(id string) string {
	return filepath.Join(c.config.RootDir, sandboxesDir, id)
}

// getVolatileSandboxRootDir returns the root directory for managing volatile sandbox files,
// e.g. named pipes.
func (c *criService) getVolatileSandboxRootDir(id string) string {
	return filepath.Join(c.config.StateDir, sandboxesDir, id)
}

// getSandboxHostname returns the hostname file path inside the sandbox root directory.
func (c *criService) getSandboxHostname(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "hostname")
}

// getSandboxHosts returns the hosts file path inside the sandbox root directory.
func (c *criService) getSandboxHosts(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "hosts")
}

// getResolvPath returns resolv.conf filepath for specified sandbox.
func (c *criService) getResolvPath(id string) string {
	return filepath.Join(c.getSandboxRootDir(id), "resolv.conf")
}

// getSandboxDevShm returns the shm file path inside the sandbox root directory.
func (c *criService) getSandboxDevShm(id string) string {
	return filepath.Join(c.getVolatileSandboxRootDir(id), "shm")
}

// makeSandboxName generates sandbox name from sandbox metadata. The name
// generated is unique as long as sandbox metadata is unique.
func makeSandboxName(s *runtime.PodSandboxMetadata) string {
	return strings.Join([]string{
		s.Name,      // 0
		s.Namespace, // 1
		s.Uid,       // 2
		strconv.FormatUint(uint64(s.Attempt), 10), // 3
	}, nameDelimiter)
}

// makeContainerName generates container name from sandbox and container metadata.
// The name generated is unique as long as the sandbox container combination is
// unique.
func makeContainerName(c *runtime.ContainerMetadata, s *runtime.PodSandboxMetadata) string {
	return strings.Join([]string{
		c.Name,      // 0: container name
		s.Name,      // 1: pod name
		s.Namespace, // 2: pod namespace
		s.Uid,       // 3: pod uid
		strconv.FormatUint(uint64(c.Attempt), 10), // 4: attempt number of creating the container
	}, nameDelimiter)
}

// getContainerRootDir returns the root directory for managing container files,
// e.g. state checkpoint.
func (c *criService) getContainerRootDir(id string) string {
	return filepath.Join(c.config.RootDir, containersDir, id)
}

// getImageVolumeHostPath returns the image volume directory for share.
func (c *criService) getImageVolumeHostPath(podID, imageID string) string {
	return filepath.Join(c.config.StateDir, imageVolumeDir, podID, imageID)
}

// getImageVolumeBaseDir returns the image volume base directory for cleanup.
func (c *criService) getImageVolumeBaseDir(podID string) string {
	return filepath.Join(c.config.StateDir, imageVolumeDir, podID)
}

// getVolatileContainerRootDir returns the root directory for managing volatile container files,
// e.g. named pipes.
func (c *criService) getVolatileContainerRootDir(id string) string {
	return filepath.Join(c.config.StateDir, containersDir, id)
}

// criContainerStateToString formats CRI container state to string.
func criContainerStateToString(state runtime.ContainerState) string {
	return runtime.ContainerState_name[int32(state)]
}

// toContainerdImage converts an image object in image store to containerd image handler.
func (c *criService) toContainerdImage(ctx context.Context, image imagestore.Image) (containerd.Image, error) {
	// image should always have at least one reference.
	if len(image.References) == 0 {
		return nil, fmt.Errorf("invalid image with no reference %q", image.ID)
	}
	return c.client.GetImage(ctx, image.References[0])
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

// validateTargetContainer checks that a container is a valid
// target for a container using PID NamespaceMode_TARGET.
// The target container must be in the same sandbox and must be running.
// Returns the target container for convenience.
func (c *criService) validateTargetContainer(sandboxID, targetContainerID string) (containerstore.Container, error) {
	targetContainer, err := c.containerStore.Get(targetContainerID)
	if err != nil {
		return containerstore.Container{}, fmt.Errorf("container %q does not exist: %w", targetContainerID, err)
	}

	targetSandboxID := targetContainer.Metadata.SandboxID
	if targetSandboxID != sandboxID {
		return containerstore.Container{},
			fmt.Errorf("container %q (sandbox %s) does not belong to sandbox %s", targetContainerID, targetSandboxID, sandboxID)
	}

	status := targetContainer.Status.Get()
	if state := status.State(); state != runtime.ContainerState_CONTAINER_RUNNING {
		return containerstore.Container{}, fmt.Errorf("container %q is not running - in state %s", targetContainerID, state)
	}

	return targetContainer, nil
}

// isInCRIMounts checks whether a destination is in CRI mount list.
func isInCRIMounts(dst string, mounts []*runtime.Mount) bool {
	for _, m := range mounts {
		if filepath.Clean(m.ContainerPath) == filepath.Clean(dst) {
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

// getRuntimeOptions get runtime options from container metadata.
func getRuntimeOptions(c containers.Container) (interface{}, error) {
	from := c.Runtime.Options
	if from == nil || from.GetValue() == nil {
		return nil, nil
	}
	opts, err := typeurl.UnmarshalAny(from)
	if err != nil {
		return nil, err
	}
	return opts, nil
}

const (
	// unknownExitCode is the exit code when exit reason is unknown.
	unknownExitCode = 255
	// unknownExitReason is the exit reason when exit reason is unknown.
	unknownExitReason = "Unknown"
)

// unknownContainerStatus returns the default container status when its status is unknown.
func unknownContainerStatus() containerstore.Status {
	return containerstore.Status{
		CreatedAt:  0,
		StartedAt:  0,
		FinishedAt: 0,
		ExitCode:   unknownExitCode,
		Reason:     unknownExitReason,
		Unknown:    true,
	}
}

// copyResourcesToStatus copys container resource contraints from spec to
// container status.
// This will need updates when new fields are added to ContainerResources.
func copyResourcesToStatus(spec *runtimespec.Spec, status containerstore.Status) containerstore.Status {
	status.Resources = &runtime.ContainerResources{}
	if spec.Linux != nil {
		status.Resources.Linux = &runtime.LinuxContainerResources{}

		if spec.Process != nil && spec.Process.OOMScoreAdj != nil {
			status.Resources.Linux.OomScoreAdj = int64(*spec.Process.OOMScoreAdj)
		}

		if spec.Linux.Resources == nil {
			return status
		}

		if spec.Linux.Resources.CPU != nil {
			if spec.Linux.Resources.CPU.Period != nil {
				status.Resources.Linux.CpuPeriod = int64(*spec.Linux.Resources.CPU.Period)
			}
			if spec.Linux.Resources.CPU.Quota != nil {
				status.Resources.Linux.CpuQuota = *spec.Linux.Resources.CPU.Quota
			}
			if spec.Linux.Resources.CPU.Shares != nil {
				status.Resources.Linux.CpuShares = int64(*spec.Linux.Resources.CPU.Shares)
			}
			status.Resources.Linux.CpusetCpus = spec.Linux.Resources.CPU.Cpus
			status.Resources.Linux.CpusetMems = spec.Linux.Resources.CPU.Mems
		}

		if spec.Linux.Resources.Memory != nil {
			if spec.Linux.Resources.Memory.Limit != nil {
				status.Resources.Linux.MemoryLimitInBytes = *spec.Linux.Resources.Memory.Limit
			}
			if spec.Linux.Resources.Memory.Swap != nil {
				status.Resources.Linux.MemorySwapLimitInBytes = *spec.Linux.Resources.Memory.Swap
			}
		}

		if spec.Linux.Resources.HugepageLimits != nil {
			hugepageLimits := make([]*runtime.HugepageLimit, 0, len(spec.Linux.Resources.HugepageLimits))
			for _, l := range spec.Linux.Resources.HugepageLimits {
				hugepageLimits = append(hugepageLimits, &runtime.HugepageLimit{
					PageSize: l.Pagesize,
					Limit:    l.Limit,
				})
			}
			status.Resources.Linux.HugepageLimits = hugepageLimits
		}

		if spec.Linux.Resources.Unified != nil {
			status.Resources.Linux.Unified = spec.Linux.Resources.Unified
		}
	}

	if spec.Windows != nil {
		status.Resources.Windows = &runtime.WindowsContainerResources{}
		if spec.Windows.Resources == nil {
			return status
		}

		if spec.Windows.Resources.CPU != nil {
			if spec.Windows.Resources.CPU.Shares != nil {
				status.Resources.Windows.CpuShares = int64(*spec.Windows.Resources.CPU.Shares)
			}
			if spec.Windows.Resources.CPU.Count != nil {
				status.Resources.Windows.CpuCount = int64(*spec.Windows.Resources.CPU.Count)
			}
			if spec.Windows.Resources.CPU.Maximum != nil {
				status.Resources.Windows.CpuMaximum = int64(*spec.Windows.Resources.CPU.Maximum)
			}
		}

		if spec.Windows.Resources.Memory != nil {
			if spec.Windows.Resources.Memory.Limit != nil {
				status.Resources.Windows.MemoryLimitInBytes = int64(*spec.Windows.Resources.Memory.Limit)
			}
		}

		// TODO: Figure out how to get RootfsSizeInBytes
	}
	return status
}

func (c *criService) generateAndSendContainerEvent(ctx context.Context, containerID string, sandboxID string, eventType runtime.ContainerEventType) {
	podSandboxStatus, err := c.getPodSandboxStatus(ctx, sandboxID)
	if err != nil {
		log.G(ctx).Warnf("Failed to get podSandbox status for container event for sandboxID %q: %v. Sending the event with nil podSandboxStatus.", sandboxID, err)
		podSandboxStatus = nil
	}
	containerStatuses, err := c.getContainerStatuses(ctx, sandboxID)
	if err != nil {
		log.G(ctx).Errorf("Failed to get container statuses for container event for sandboxID %q: %v", sandboxID, err)
	}

	event := runtime.ContainerEventResponse{
		ContainerId:        containerID,
		ContainerEventType: eventType,
		CreatedAt:          time.Now().UnixNano(),
		PodSandboxStatus:   podSandboxStatus,
		ContainersStatuses: containerStatuses,
	}

	c.containerEventsQ.Send(event)
}

func (c *criService) getPodSandboxRuntime(sandboxID string) (runtime criconfig.Runtime, err error) {
	sandbox, err := c.sandboxStore.Get(sandboxID)
	if err != nil {
		return criconfig.Runtime{}, err
	}
	runtime, err = c.config.GetSandboxRuntime(sandbox.Config, sandbox.Metadata.RuntimeHandler)
	if err != nil {
		return criconfig.Runtime{}, err
	}
	return runtime, nil
}

func (c *criService) getPodSandboxStatus(ctx context.Context, podSandboxID string) (*runtime.PodSandboxStatus, error) {
	request := &runtime.PodSandboxStatusRequest{PodSandboxId: podSandboxID}
	response, err := c.PodSandboxStatus(ctx, request)
	if err != nil {
		return nil, err
	}
	return response.GetStatus(), nil
}

func (c *criService) getContainerStatuses(ctx context.Context, podSandboxID string) ([]*runtime.ContainerStatus, error) {
	response, err := c.ListContainers(ctx, &runtime.ListContainersRequest{
		Filter: &runtime.ContainerFilter{
			PodSandboxId: podSandboxID,
		},
	})
	if err != nil {
		return nil, err
	}
	containerStatuses := []*runtime.ContainerStatus{}
	for _, container := range response.Containers {
		statusResp, err := c.ContainerStatus(ctx, &runtime.ContainerStatusRequest{
			ContainerId: container.Id,
			Verbose:     false,
		})
		if err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		containerStatuses = append(containerStatuses, statusResp.GetStatus())
	}
	return containerStatuses, nil
}

// hostNetwork handles checking if host networking was requested.
func hostNetwork(config *runtime.PodSandboxConfig) bool {
	var hostNet bool
	switch goruntime.GOOS {
	case "windows":
		// Windows HostProcess pods can only run on the host network
		hostNet = config.GetWindows().GetSecurityContext().GetHostProcess()
	case "darwin":
		// No CNI on Darwin yet.
		hostNet = true
	default:
		// Even on other platforms, the logic containerd uses is to check if NamespaceMode == NODE.
		// So this handles Linux, as well as any other platforms not governed by the cases above
		// that have special quirks.
		hostNet = config.GetLinux().GetSecurityContext().GetNamespaceOptions().GetNetwork() == runtime.NamespaceMode_NODE
	}
	return hostNet
}

// getCgroupsPath generates container cgroups path.
func getCgroupsPath(cgroupsParent, id string) string {
	base := path.Base(cgroupsParent)
	if strings.HasSuffix(base, ".slice") {
		// For a.slice/b.slice/c.slice, base is c.slice.
		// runc systemd cgroup path format is "slice:prefix:name".
		return strings.Join([]string{base, "cri-containerd", id}, ":")
	}
	return filepath.Join(cgroupsParent, id)
}

func toLabel(selinuxOptions *runtime.SELinuxOption) ([]string, error) {
	var labels []string

	if selinuxOptions == nil {
		return nil, nil
	}
	if err := checkSelinuxLevel(selinuxOptions.Level); err != nil {
		return nil, err
	}
	if selinuxOptions.User != "" {
		labels = append(labels, "user:"+selinuxOptions.User)
	}
	if selinuxOptions.Role != "" {
		labels = append(labels, "role:"+selinuxOptions.Role)
	}
	if selinuxOptions.Type != "" {
		labels = append(labels, "type:"+selinuxOptions.Type)
	}
	if selinuxOptions.Level != "" {
		labels = append(labels, "level:"+selinuxOptions.Level)
	}

	return labels, nil
}

func checkSelinuxLevel(level string) error {
	if len(level) == 0 {
		return nil
	}

	matched, err := regexp.MatchString(`^s\d(-s\d)??(:c\d{1,4}(\.c\d{1,4})?(,c\d{1,4}(\.c\d{1,4})?)*)?$`, level)
	if err != nil {
		return fmt.Errorf("the format of 'level' %q is not correct: %w", level, err)
	}
	if !matched {
		return fmt.Errorf("the format of 'level' %q is not correct", level)
	}
	return nil
}

func parseUsernsIDMap(runtimeIDMap []*runtime.IDMapping) ([]runtimespec.LinuxIDMapping, error) {
	var m []runtimespec.LinuxIDMapping

	if len(runtimeIDMap) == 0 {
		return m, nil
	}

	if len(runtimeIDMap) > 1 {
		// We only accept 1 line, because containerd.WithRemappedSnapshot() only supports that.
		return m, fmt.Errorf("only one mapping line supported, got %v mapping lines", len(runtimeIDMap))
	}

	// We know len is 1 now.
	if runtimeIDMap[0] == nil {
		return m, nil
	}
	uidMap := *runtimeIDMap[0]

	if uidMap.Length < 1 {
		return m, fmt.Errorf("invalid mapping length: %v", uidMap.Length)
	}

	m = []runtimespec.LinuxIDMapping{
		{
			ContainerID: uidMap.ContainerId,
			HostID:      uidMap.HostId,
			Size:        uidMap.Length,
		},
	}

	return m, nil
}

func parseUsernsIDs(userns *runtime.UserNamespace) (uids, gids []runtimespec.LinuxIDMapping, retErr error) {
	if userns == nil {
		// If userns is not set, the kubelet doesn't support this option
		// and we should just fallback to no userns. This is completely
		// valid.
		return nil, nil, nil
	}

	uids, err := parseUsernsIDMap(userns.GetUids())
	if err != nil {
		return nil, nil, fmt.Errorf("UID mapping: %w", err)
	}

	gids, err = parseUsernsIDMap(userns.GetGids())
	if err != nil {
		return nil, nil, fmt.Errorf("GID mapping: %w", err)
	}

	switch mode := userns.GetMode(); mode {
	case runtime.NamespaceMode_NODE:
		if len(uids) != 0 || len(gids) != 0 {
			return nil, nil, fmt.Errorf("can't use user namespace mode %q with mappings. Got %v UID mappings and %v GID mappings", mode, len(uids), len(gids))
		}
	case runtime.NamespaceMode_POD:
		// This is valid, we will handle it in WithPodNamespaces().
		if len(uids) == 0 || len(gids) == 0 {
			return nil, nil, fmt.Errorf("can't use user namespace mode %q without UID and GID mappings", mode)
		}
	default:
		return nil, nil, fmt.Errorf("unsupported user namespace mode: %q", mode)
	}

	return uids, gids, nil
}

// sameUsernsConfig checks if the userns configs are the same. If the mappings
// on each config are the same but in different order, it returns false.
// XXX: If the runtime.UserNamespace struct changes, we should update this
// function accordingly.
func sameUsernsConfig(a, b *runtime.UserNamespace) bool {
	// If both are nil, they are the same.
	if a == nil && b == nil {
		return true
	}
	// If only one is nil, they are different.
	if a == nil || b == nil {
		return false
	}
	// At this point, a is not nil nor b.

	if a.GetMode() != b.GetMode() {
		return false
	}

	aUids, aGids, err := parseUsernsIDs(a)
	if err != nil {
		return false
	}
	bUids, bGids, err := parseUsernsIDs(b)
	if err != nil {
		return false
	}

	if !sameMapping(aUids, bUids) {
		return false
	}
	if !sameMapping(aGids, bGids) {
		return false
	}
	return true
}

// sameMapping checks if the mappings are the same. If the mappings are the same
// but in different order, it returns false.
func sameMapping(a, b []runtimespec.LinuxIDMapping) bool {
	if len(a) != len(b) {
		return false
	}

	for x := range a {
		if a[x].ContainerID != b[x].ContainerID {
			return false
		}
		if a[x].HostID != b[x].HostID {
			return false
		}
		if a[x].Size != b[x].Size {
			return false
		}
	}
	return true
}
