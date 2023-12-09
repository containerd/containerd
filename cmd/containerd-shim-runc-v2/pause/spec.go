//go:build linux

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

package pause

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/pkg/cri/annotations"
	customopts "github.com/containerd/containerd/v2/pkg/cri/opts"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/pkg/userns"
)

const defaultSandboxOOMAdj = -998

func pauseContainerSpec(id string, config *runtime.PodSandboxConfig, nsPath string) (_ *runtimespec.Spec, retErr error) {
	specOpts := []oci.SpecOpts{
		oci.WithoutRunMount,
		customopts.WithoutDefaultSecuritySettings,
		customopts.WithRelativeRoot("rootfs"),
		oci.WithRootFSReadonly(),
		oci.WithHostname(config.GetHostname()),
	}
	specOpts = append(specOpts, oci.WithProcessArgs("/pause"))

	if config.GetLinux().GetCgroupParent() != "" {
		cgroupsPath := getCgroupsPath(config.GetLinux().GetCgroupParent(), id)
		specOpts = append(specOpts, oci.WithCgroup(cgroupsPath))
	}

	// Set namespace options.
	var (
		securityContext = config.GetLinux().GetSecurityContext()
		nsOptions       = securityContext.GetNamespaceOptions()
	)
	if nsOptions.GetNetwork() == runtime.NamespaceMode_NODE {
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.NetworkNamespace))
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.UTSNamespace))
	} else {
		specOpts = append(specOpts, oci.WithLinuxNamespace(
			runtimespec.LinuxNamespace{
				Type: runtimespec.NetworkNamespace,
				Path: nsPath,
			}))
	}
	if nsOptions.GetPid() == runtime.NamespaceMode_NODE {
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.PIDNamespace))
	}
	if nsOptions.GetIpc() == runtime.NamespaceMode_NODE {
		specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.IPCNamespace))
	}

	usernsOpts := nsOptions.GetUsernsOptions()
	uids, gids, err := parseUsernsIDs(usernsOpts)
	var usernsEnabled bool
	if err != nil {
		return nil, fmt.Errorf("user namespace configuration: %w", err)
	}

	if usernsOpts != nil {
		switch mode := usernsOpts.GetMode(); mode {
		case runtime.NamespaceMode_NODE:
			specOpts = append(specOpts, customopts.WithoutNamespace(runtimespec.UserNamespace))
		case runtime.NamespaceMode_POD:
			specOpts = append(specOpts, oci.WithUserNamespace(uids, gids))
			usernsEnabled = true
		default:
			return nil, fmt.Errorf("unsupported user namespace mode: %q", mode)
		}
	}

	supplementalGroups := securityContext.GetSupplementalGroups()
	specOpts = append(specOpts,
		customopts.WithSupplementalGroups(supplementalGroups),
	)

	// Add sysctls
	sysctls := config.GetLinux().GetSysctls()
	if sysctls == nil {
		sysctls = make(map[string]string)
	}
	_, ipUnprivilegedPortStart := sysctls["net.ipv4.ip_unprivileged_port_start"]
	_, pingGroupRange := sysctls["net.ipv4.ping_group_range"]
	if nsOptions.GetNetwork() != runtime.NamespaceMode_NODE {
		if !ipUnprivilegedPortStart {
			sysctls["net.ipv4.ip_unprivileged_port_start"] = "0"
		}
		if !pingGroupRange && !userns.RunningInUserNS() && !usernsEnabled {
			sysctls["net.ipv4.ping_group_range"] = "0 2147483647"
		}
	}
	specOpts = append(specOpts, customopts.WithSysctls(sysctls))

	if res := config.GetLinux().GetResources(); res != nil {
		specOpts = append(specOpts,
			customopts.WithAnnotation(annotations.SandboxCPUPeriod, strconv.FormatInt(res.CpuPeriod, 10)),
			customopts.WithAnnotation(annotations.SandboxCPUQuota, strconv.FormatInt(res.CpuQuota, 10)),
			customopts.WithAnnotation(annotations.SandboxCPUShares, strconv.FormatInt(res.CpuShares, 10)),
			customopts.WithAnnotation(annotations.SandboxMem, strconv.FormatInt(res.MemoryLimitInBytes, 10)))
	}

	specOpts = append(specOpts, customopts.WithPodOOMScoreAdj(defaultSandboxOOMAdj, false))

	for pKey, pValue := range config.Annotations {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	specOpts = append(specOpts, annotations.DefaultCRIAnnotations(id, "", "", config, true)...)

	ctx := ctrdutil.NamespacedContext()
	container := &containers.Container{ID: id}
	spec, err := oci.GenerateSpec(ctx, nil, container, specOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spec: %w", err)
	}
	return spec, nil
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
