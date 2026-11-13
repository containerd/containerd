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
	// containers (shareProcessNamespace); containerd-sandbox-init is then
	// run as its PID 1 (see initProcess).
	SharedPID bool
	// NetNSPath is the network namespace CRI created for the pod.
	NetNSPath string
	// Sysctls is the effective set of namespaced sysctls to apply.
	Sysctls map[string]string
	// CgroupParent is the pod cgroup as a cgroup v2 group path. The sandbox
	// reports its metrics and places the setup runc container under it.
	// kubelet spells it that way with both cgroup drivers: the systemd
	// driver hands over the expanded slice path, which CRI shortens to its
	// base for runc.
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
		// user namespace. The setup runc container can create them there,
		// but the shim does not configure that yet.
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

// defaultSysctls are applied to pods with their own network namespace unless
// the pod sets them, matching what containerd applies to pause sandboxes with
// its default CRI configuration (enable_unprivileged_ports and
// enable_unprivileged_icmp both on).
var defaultSysctls = map[string]string{
	"net.ipv4.ip_unprivileged_port_start": "0",
	"net.ipv4.ping_group_range":           "0 2147483647",
}

// ipcSysctls are the IPC namespace sysctls runc accepts, besides fs.mqueue.*.
var ipcSysctls = map[string]bool{
	"kernel.msgmax":          true,
	"kernel.msgmnb":          true,
	"kernel.msgmni":          true,
	"kernel.sem":             true,
	"kernel.shmall":          true,
	"kernel.shmmax":          true,
	"kernel.shmmni":          true,
	"kernel.shm_rmid_forced": true,
}

// validateSysctl accepts the sysctls runc applies to the setup runc container
// (see runcSpec) and rejects the rest before runc runs so that the pod gets a
// clear error. It follows runc's rules: net.* needs the pod network
// namespace, the IPC sysctls runc lists need the pod IPC namespace, and
// kernel.domainname needs the pod UTS namespace. kernel.hostname conflicts
// with the pod hostname, user.* belongs to a user namespace, which the
// sandbox does not have, and runc rejects any other sysctl as not
// namespaced. It returns the key in dotted form (see sysctlKey).
func validateSysctl(key string, hostNetwork, hostIPC bool) (string, error) {
	dotted, err := sysctlKey(key)
	if err != nil {
		return "", err
	}
	switch {
	case strings.HasPrefix(dotted, "net."):
		if hostNetwork {
			return "", fmt.Errorf("sysctl %q is not allowed for a pod using the host network: %w", key, errdefs.ErrInvalidArgument)
		}
	case ipcSysctls[dotted], strings.HasPrefix(dotted, "fs.mqueue."):
		if hostIPC {
			return "", fmt.Errorf("sysctl %q is not allowed for a pod using the host IPC namespace: %w", key, errdefs.ErrInvalidArgument)
		}
	case dotted == "kernel.domainname":
		if hostNetwork {
			return "", fmt.Errorf("sysctl %q is not allowed for a pod using the host network (and UTS) namespace: %w", key, errdefs.ErrInvalidArgument)
		}
	case dotted == "kernel.hostname":
		return "", fmt.Errorf("sysctl %q is not allowed: the pod hostname sets it: %w", key, errdefs.ErrInvalidArgument)
	case strings.HasPrefix(dotted, "user."):
		return "", fmt.Errorf("sysctl %q: user.* sysctls are not supported by %s: %w", key, BinaryName, errdefs.ErrNotImplemented)
	default:
		return "", fmt.Errorf("sysctl %q cannot be set for a pod: it is not namespaced, or runc does not allow it: %w", key, errdefs.ErrInvalidArgument)
	}
	return dotted, nil
}

// effectiveSysctls validates the pod sysctls and adds the defaults. The keys
// of the result are in dotted form; a sysctl the pod sets under two spellings
// does not have a single value and is rejected.
func effectiveSysctls(pod map[string]string, hostNetwork, hostIPC bool) (map[string]string, error) {
	out := make(map[string]string, len(pod)+len(defaultSysctls))
	for k, v := range pod {
		dotted, err := validateSysctl(k, hostNetwork, hostIPC)
		if err != nil {
			return nil, err
		}
		if _, ok := out[dotted]; ok {
			return nil, fmt.Errorf("sysctl %q is set more than once: %w", dotted, errdefs.ErrInvalidArgument)
		}
		out[dotted] = v
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

// sysctlKey returns a sysctl key, spelled as in sysctl.d(5) with either
// separator, in dotted form. A name containing the other separator (the VLAN
// interface of net/ipv4/conf/eth0.100/rp_filter) is rejected: runc maps every
// "." to "/", as it does for a pause sandbox.
func sysctlKey(key string) (string, error) {
	sep := "."
	if i := strings.IndexAny(key, "./"); i >= 0 && key[i] == '/' {
		sep = "/"
	}
	parts := strings.Split(key, sep)
	for _, c := range parts {
		if c == "" || c == "." || c == ".." || strings.ContainsRune(c, 0) {
			return "", fmt.Errorf("invalid sysctl key %q: %w", key, errdefs.ErrInvalidArgument)
		}
		if strings.ContainsAny(c, "./") {
			return "", fmt.Errorf("sysctl %q: a name containing %q is not supported, runc would set the wrong file: %w", key, c, errdefs.ErrNotImplemented)
		}
	}
	return strings.Join(parts, "."), nil
}

// sandboxSpec synthesizes the sandbox spec CRI and NRI consume. It is not the
// spec of a process: it describes the namespaces the sandbox holds, from what
// was actually pinned, the sysctls applied to them, and the pod shared files
// as mounts. A namespace the pod shares with the host has no entry. Linux is
// always set, which is how CRI tells a sandbox without a process from an
// older Sandbox API shim that returns no spec.
//
// The PID namespace entry has a path only for a pod that shares its PID
// namespace (NamespaceMode_POD, the CRI default and kubelet's
// shareProcessNamespace): pins.PID, the namespace containerd-sandbox-init is
// PID 1 of. Otherwise the sandbox does not hold a PID namespace, and the
// entry, without a path, declares that each container gets a new one of its
// own; CRI honors the declaration. A host PID pod does not get an entry.
func sandboxSpec(cfg *Config, pins Pins, mounts []specs.Mount) *specs.Spec {
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
	switch {
	case cfg.HostPID:
	case pins.PID != "":
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{Type: specs.PIDNamespace, Path: pins.PID})
	default:
		s.Linux.Namespaces = append(s.Linux.Namespaces, specs.LinuxNamespace{Type: specs.PIDNamespace})
	}
	return s
}
