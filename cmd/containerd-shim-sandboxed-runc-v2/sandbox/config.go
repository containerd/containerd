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

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	api "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/v2/internal/cri/sandboxfiles"
)

// Config is what the sandbox shim needs to know about a pod. It is derived
// from the CRI PodSandboxConfig that CRI passes as the sandbox options.
type Config struct {
	// ID is the sandbox id.
	ID string
	// Hostname is the pod hostname, the node hostname when the pod sets none.
	Hostname string
	// HostNetwork means the pod uses the host network and UTS namespaces; the
	// sandbox then holds neither and NetNSPath is empty.
	HostNetwork bool
	// HostIPC means the pod uses the host IPC namespace and the host /dev/shm.
	HostIPC bool
	// HostPID means the containers of the pod use the host PID namespace.
	HostPID bool
	// SharedPID means the pod asked for one PID namespace shared by its
	// containers, which this shim cannot provide yet (see buildSpec).
	SharedPID bool
	// NetNSPath is the network namespace CRI created for the pod.
	NetNSPath string
	// Sysctls is the effective set of namespaced sysctls to apply.
	Sysctls map[string]string
	// CgroupParent is the pod cgroup, used for metrics.
	CgroupParent string
	// DNS is the pod DNS configuration; nil means the host resolv.conf.
	DNS *runtime.DNSConfig
	// ShmSize is the size of the pod /dev/shm tmpfs.
	ShmSize int64
	// Annotations are the annotations of the synthesized sandbox spec.
	Annotations map[string]string
}

// ConfigFromRequest derives the sandbox configuration from a create request.
// It rejects what this shim does not support yet with ErrNotImplemented so the
// error is explicit rather than a pod that silently lacks isolation.
func ConfigFromRequest(r *api.CreateSandboxRequest) (*Config, error) {
	id := r.GetSandboxID()
	if r.GetOptions() == nil {
		return nil, fmt.Errorf("sandbox %q: no options; %s expects the CRI PodSandboxConfig: %w", id, BinaryName, errdefs.ErrInvalidArgument)
	}
	// TODO: define a containerd-owned sandbox configuration message for the
	// handoff (hostname, namespace modes, the effective sysctls including the
	// enable_unprivileged_ports/enable_unprivileged_icmp defaults CRI applies
	// to pause, DNS, cgroup parent, labels) and have CRI send it as an
	// extension of the create request; a sandboxed shim should not depend on
	// the CRI layer and its PodSandboxConfig, nor re-derive CRI policy.
	var pc runtime.PodSandboxConfig
	if err := typeurl.UnmarshalTo(r.GetOptions(), &pc); err != nil {
		return nil, fmt.Errorf("sandbox %q: options are not a CRI PodSandboxConfig: %w", id, err)
	}
	return configFromPodSandboxConfig(id, r.GetNetnsPath(), r.GetAnnotations(), &pc)
}

func configFromPodSandboxConfig(id, netns string, reqAnnotations map[string]string, pc *runtime.PodSandboxConfig) (*Config, error) {
	nsOpts := pc.GetLinux().GetSecurityContext().GetNamespaceOptions()
	cfg := &Config{
		ID:           id,
		Hostname:     pc.GetHostname(),
		HostNetwork:  nsOpts.GetNetwork() == runtime.NamespaceMode_NODE,
		HostIPC:      nsOpts.GetIpc() == runtime.NamespaceMode_NODE,
		HostPID:      nsOpts.GetPid() == runtime.NamespaceMode_NODE,
		SharedPID:    nsOpts.GetPid() == runtime.NamespaceMode_POD,
		NetNSPath:    netns,
		CgroupParent: pc.GetLinux().GetCgroupParent(),
		DNS:          pc.GetDnsConfig(),
		ShmSize:      sandboxfiles.DefaultShmSize,
	}

	if nsOpts.GetPid() == runtime.NamespaceMode_TARGET {
		return nil, fmt.Errorf("sandbox %q: PID namespace mode TARGET is not valid for a pod sandbox: %w", id, errdefs.ErrInvalidArgument)
	}
	if u := nsOpts.GetUsernsOptions(); u != nil && u.GetMode() != runtime.NamespaceMode_NODE {
		// The namespaces of a user namespaced pod must be owned by the pod
		// user namespace, which a multithreaded shim cannot enter; CRI has
		// to create them. That is a follow-up.
		return nil, fmt.Errorf("sandbox %q: user namespaces are not supported yet by %s: %w", id, BinaryName, errdefs.ErrNotImplemented)
	}

	if cfg.HostNetwork {
		cfg.NetNSPath = ""
	} else if cfg.NetNSPath == "" {
		return nil, fmt.Errorf("sandbox %q: no network namespace path for a pod with its own network namespace: %w", id, errdefs.ErrInvalidArgument)
	}

	if cfg.Hostname == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("sandbox %q: failed to get the node hostname: %w", id, err)
		}
		cfg.Hostname = hostname
	}

	sysctls, err := effectiveSysctls(pc.GetLinux().GetSysctls(), cfg.HostNetwork, cfg.HostIPC)
	if err != nil {
		return nil, fmt.Errorf("sandbox %q: %w", id, err)
	}
	cfg.Sysctls = sysctls
	cfg.Annotations = reqAnnotations
	return cfg, nil
}

// Sysctls are applied by the shim thread that created the pod namespaces (for
// pause, runc applied them inside the pause container), so only namespaced
// sysctls of namespaces the pod owns can be honored.

const sysctlRoot = "/proc/sys"

// defaultSysctls are applied to pods with their own network namespace unless
// the pod sets them, matching what containerd applies to pause sandboxes with
// its default CRI configuration (enable_unprivileged_ports and
// enable_unprivileged_icmp both on).
var defaultSysctls = map[string]string{
	"net.ipv4.ip_unprivileged_port_start": "0",
	"net.ipv4.ping_group_range":           "0 2147483647",
}

// validateSysctl accepts the namespaced sysctls the shim can apply from a host
// thread that entered the pod namespaces, and rejects the rest: user.* is
// scoped to a user namespace the shim never enters, a sysctl of a namespace
// the pod shares with the host would change the host, and anything else is
// not namespaced at all.
func validateSysctl(key string, hostNetwork, hostIPC bool) error {
	if _, err := sysctlPath(key); err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(key, "net."):
		if hostNetwork {
			return fmt.Errorf("sysctl %q is not allowed for a pod using the host network: %w", key, errdefs.ErrInvalidArgument)
		}
	case strings.HasPrefix(key, "kernel.shm"), strings.HasPrefix(key, "kernel.msg"), key == "kernel.sem", strings.HasPrefix(key, "fs.mqueue."):
		if hostIPC {
			return fmt.Errorf("sysctl %q is not allowed for a pod using the host IPC namespace: %w", key, errdefs.ErrInvalidArgument)
		}
	case key == "kernel.hostname", key == "kernel.domainname":
		if hostNetwork {
			return fmt.Errorf("sysctl %q is not allowed for a pod using the host network (and UTS) namespace: %w", key, errdefs.ErrInvalidArgument)
		}
	case strings.HasPrefix(key, "user."):
		return fmt.Errorf("sysctl %q: user.* sysctls are not supported by %s: %w", key, BinaryName, errdefs.ErrNotImplemented)
	default:
		return fmt.Errorf("sysctl %q is not namespaced and cannot be set for a pod: %w", key, errdefs.ErrInvalidArgument)
	}
	return nil
}

// effectiveSysctls validates the pod sysctls and adds the defaults.
func effectiveSysctls(pod map[string]string, hostNetwork, hostIPC bool) (map[string]string, error) {
	out := make(map[string]string, len(pod)+len(defaultSysctls))
	for k, v := range pod {
		if err := validateSysctl(k, hostNetwork, hostIPC); err != nil {
			return nil, err
		}
		out[k] = v
	}
	if !hostNetwork {
		for k, v := range defaultSysctls {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

// sysctlPath maps a dotted sysctl key to its /proc/sys file the way runc does
// (every dot becomes a slash, which also spells interface names with dots the
// way procfs does) and refuses keys that would resolve outside /proc/sys.
func sysctlPath(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("empty sysctl key: %w", errdefs.ErrInvalidArgument)
	}
	p := filepath.Join(sysctlRoot, strings.ReplaceAll(key, ".", "/"))
	if !strings.HasPrefix(p, sysctlRoot+"/") {
		return "", fmt.Errorf("invalid sysctl key %q: %w", key, errdefs.ErrInvalidArgument)
	}
	return p, nil
}

// buildSpec synthesizes the sandbox spec CRI and NRI consume. It is not the
// spec of a process (there is none): it describes the namespaces the sandbox
// holds, from what was actually pinned, the sysctls applied to them, and the
// pod shared files as mounts. A namespace the pod shares with the host has no
// entry. Linux is always set, which is how CRI tells a sandbox without a
// process from an older Sandbox API shim that returns no spec.
//
// The PID namespace entry has no path: the sandbox holds none. With a pause
// container the pod PID namespace is the one pause is PID 1 of, and a pod
// that asks to share it (NamespaceMode_POD, the CRI default and kubelet's
// shareProcessNamespace) joins its containers to it. Holding a shared PID
// namespace needs a durable PID 1 (sandbox-init, a follow-up); until then
// the entry declares that each container gets a new PID namespace of its
// own, and CRI honors that declaration instead of failing.
func buildSpec(cfg *Config, pins Pins, mounts []specs.Mount) *specs.Spec {
	s := &specs.Spec{
		Version:     specs.Version,
		Hostname:    cfg.Hostname,
		Annotations: cfg.Annotations,
		Mounts:      mounts,
		Linux: &specs.Linux{
			Sysctl: cfg.Sysctls,
		},
	}
	if cfg.NetNSPath != "" {
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: cfg.NetNSPath})
	}
	if pins.IPC != "" {
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{Type: specs.IPCNamespace, Path: pins.IPC})
	}
	if pins.UTS != "" {
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{Type: specs.UTSNamespace, Path: pins.UTS})
	}
	if !cfg.HostPID {
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{Type: specs.PIDNamespace})
	}
	return s
}
