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
	"os"
	"path/filepath"
	"strings"
	"testing"

	api "github.com/containerd/containerd/api/runtime/sandbox/v1"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	testID    = "sandbox-id"
	testNetNS = "/var/run/netns/cni-test"
)

func testPodConfig() *runtime.PodSandboxConfig {
	return &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name:      "pod",
			Uid:       "pod-uid",
			Namespace: "pod-ns",
		},
		Hostname:     "pod-hostname",
		LogDirectory: "/var/log/pods/pod",
		DnsConfig: &runtime.DNSConfig{
			Servers: []string{"10.0.0.10"},
		},
		Linux: &runtime.LinuxPodSandboxConfig{
			CgroupParent: "/kubepods/pod",
			SecurityContext: &runtime.LinuxSandboxSecurityContext{
				NamespaceOptions: &runtime.NamespaceOption{},
			},
		},
	}
}

func createRequest(t *testing.T, pc *runtime.PodSandboxConfig, netns string) *api.CreateSandboxRequest {
	t.Helper()
	req := &api.CreateSandboxRequest{
		SandboxID:   testID,
		BundlePath:  "/bundle",
		NetnsPath:   netns,
		Annotations: map[string]string{"extra": "annotation"},
	}
	if pc != nil {
		any, err := typeurl.MarshalAnyToProto(pc)
		require.NoError(t, err)
		req.Options = any
	}
	return req
}

func TestConfigFromRequest(t *testing.T) {
	t.Run("options are required", func(t *testing.T) {
		_, err := ConfigFromRequest(createRequest(t, nil, testNetNS))
		require.Error(t, err)
		assert.True(t, errdefs.IsInvalidArgument(err))
	})

	t.Run("pod namespaces", func(t *testing.T) {
		cfg, err := ConfigFromRequest(createRequest(t, testPodConfig(), testNetNS))
		require.NoError(t, err)
		assert.Equal(t, testID, cfg.ID)
		assert.Equal(t, "pod-hostname", cfg.Hostname)
		assert.False(t, cfg.HostNetwork)
		assert.False(t, cfg.HostIPC)
		assert.False(t, cfg.HostPID)
		assert.Equal(t, testNetNS, cfg.NetNSPath)
		assert.Equal(t, "/kubepods/pod", cfg.CgroupParent)
		assert.Equal(t, []string{"10.0.0.10"}, cfg.DNS.GetServers())
		assert.Equal(t, defaultSysctls, cfg.Sysctls, "the pause defaults apply to a pod with its own network namespace")
		assert.Equal(t, map[string]string{"extra": "annotation"}, cfg.Annotations, "the annotations of the request")
	})

	t.Run("pod network requires a network namespace path", func(t *testing.T) {
		_, err := ConfigFromRequest(createRequest(t, testPodConfig(), ""))
		require.Error(t, err)
		assert.True(t, errdefs.IsInvalidArgument(err))
	})

	t.Run("host network", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.SecurityContext.NamespaceOptions.Network = runtime.NamespaceMode_NODE
		cfg, err := ConfigFromRequest(createRequest(t, pc, ""))
		require.NoError(t, err)
		assert.True(t, cfg.HostNetwork)
		assert.Empty(t, cfg.NetNSPath)
		assert.Empty(t, cfg.Sysctls, "no network defaults for a pod in the host network namespace")

		pc.Linux.Sysctls = map[string]string{"net.ipv4.ip_forward": "1"}
		_, err = ConfigFromRequest(createRequest(t, pc, ""))
		require.Error(t, err, "a net sysctl would change the host")
		assert.True(t, errdefs.IsInvalidArgument(err))
	})

	t.Run("host ipc", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.SecurityContext.NamespaceOptions.Ipc = runtime.NamespaceMode_NODE
		cfg, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		assert.True(t, cfg.HostIPC)

		pc.Linux.Sysctls = map[string]string{"kernel.shm_rmid_forced": "1"}
		_, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err, "an IPC sysctl would change the host")
		assert.True(t, errdefs.IsInvalidArgument(err))
	})

	t.Run("host pid", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.SecurityContext.NamespaceOptions.Pid = runtime.NamespaceMode_NODE
		cfg, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		assert.True(t, cfg.HostPID)
	})

	t.Run("pod sysctls win over the defaults", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.Sysctls = map[string]string{
			"net.ipv4.ip_unprivileged_port_start": "1024",
			"kernel.shm_rmid_forced":              "1",
			"fs.mqueue.msg_max":                   "20",
		}
		cfg, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"net.ipv4.ip_unprivileged_port_start": "1024",
			"net.ipv4.ping_group_range":           "0 2147483647",
			"kernel.shm_rmid_forced":              "1",
			"fs.mqueue.msg_max":                   "20",
		}, cfg.Sysctls)
	})

	t.Run("slash separated sysctls", func(t *testing.T) {
		// kubelet passes sysctl names with either separator through as
		// written; they are classified and kept in dotted form.
		pc := testPodConfig()
		pc.Linux.Sysctls = map[string]string{"net/ipv4/ping_group_range": "1 1", "kernel/shm_rmid_forced": "1"}
		cfg, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"net.ipv4.ip_unprivileged_port_start": "0",
			"net.ipv4.ping_group_range":           "1 1",
			"kernel.shm_rmid_forced":              "1",
		}, cfg.Sysctls)

		pc.Linux.SecurityContext.NamespaceOptions.Ipc = runtime.NamespaceMode_NODE
		_, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err, "an IPC sysctl would change the host, however it is spelled")
		assert.True(t, errdefs.IsInvalidArgument(err))

		// One sysctl under both spellings does not have a single value to apply.
		pc = testPodConfig()
		pc.Linux.Sysctls = map[string]string{"net.ipv4.ip_forward": "1", "net/ipv4/ip_forward": "0"}
		_, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		assert.ErrorIs(t, err, errdefs.ErrInvalidArgument)
	})

	t.Run("shared pid namespace is accepted but not shared", func(t *testing.T) {
		// NamespaceMode_POD is the zero value, so it is what a pod config
		// without explicit namespace options (crictl, critest) asks for.
		pc := testPodConfig()
		pc.Linux.SecurityContext.NamespaceOptions.Pid = runtime.NamespaceMode_POD
		cfg, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		assert.True(t, cfg.SharedPID)

		pc.Linux.SecurityContext.NamespaceOptions.Pid = runtime.NamespaceMode_CONTAINER
		cfg, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		assert.False(t, cfg.SharedPID)
	})

	t.Run("target pid namespace is invalid for a sandbox", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.SecurityContext.NamespaceOptions.Pid = runtime.NamespaceMode_TARGET
		_, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err)
		assert.True(t, errdefs.IsInvalidArgument(err))
	})

	t.Run("user namespaces are not supported yet", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.SecurityContext.NamespaceOptions.UsernsOptions = &runtime.UserNamespace{Mode: runtime.NamespaceMode_POD}
		_, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err)
		assert.True(t, errdefs.IsNotImplemented(err))

		pc.Linux.SecurityContext.NamespaceOptions.UsernsOptions = &runtime.UserNamespace{Mode: runtime.NamespaceMode_NODE}
		_, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err, "the host user namespace is the default")
	})

	t.Run("unsupported sysctls", func(t *testing.T) {
		pc := testPodConfig()
		pc.Linux.Sysctls = map[string]string{"user.max_user_namespaces": "10"}
		_, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err)
		assert.True(t, errdefs.IsNotImplemented(err), "user.* needs the pod user namespace")

		pc.Linux.Sysctls = map[string]string{"vm.swappiness": "10"}
		_, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err)
		assert.True(t, errdefs.IsInvalidArgument(err), "not namespaced")

		pc.Linux.Sysctls = map[string]string{".": "x"}
		_, err = ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.Error(t, err)
		assert.True(t, errdefs.IsInvalidArgument(err), "not a key")
	})

	t.Run("hostname defaults to the node hostname", func(t *testing.T) {
		pc := testPodConfig()
		pc.Hostname = ""
		cfg, err := ConfigFromRequest(createRequest(t, pc, testNetNS))
		require.NoError(t, err)
		hostname, err := os.Hostname()
		require.NoError(t, err)
		assert.Equal(t, hostname, cfg.Hostname)
	})
}

func TestSysctlKey(t *testing.T) {
	// Either separator, as in sysctl.d(5): the first one separates the
	// components. The dotted form is what runc applies.
	for key, want := range map[string]string{
		"net.ipv4.ip_unprivileged_port_start": "net.ipv4.ip_unprivileged_port_start",
		"net/ipv4/ip_unprivileged_port_start": "net.ipv4.ip_unprivileged_port_start",
		"kernel/shm_rmid_forced":              "kernel.shm_rmid_forced",
	} {
		dotted, err := sysctlKey(key)
		require.NoError(t, err, "key %q", key)
		assert.Equal(t, want, dotted, "key %q", key)
	}

	// A name containing the other character, such as a VLAN interface,
	// would make runc set the wrong file.
	for _, key := range []string{"net.ipv4.conf.eth0/100.rp_filter", "net/ipv4/conf/eth0.100/rp_filter"} {
		_, err := sysctlKey(key)
		assert.ErrorIs(t, err, errdefs.ErrNotImplemented, "key %q", key)
	}

	// No component may be empty, "." or "..", nor hold a NUL byte.
	for _, key := range []string{"", ".", "..", "/", "net.", ".net.ipv4.ip_forward", "net..ipv4.ip_forward", "net/../kernel/shm_rmid_forced", "net.\x00"} {
		_, err := sysctlKey(key)
		assert.ErrorIs(t, err, errdefs.ErrInvalidArgument, "key %q", key)
	}
}

// FuzzSysctlKey checks that an accepted key names a plain path under
// /proc/sys once runc maps its dots to slashes.
func FuzzSysctlKey(f *testing.F) {
	for _, key := range []string{"net.ipv4.ip_forward", "net/ipv4/ip_forward", "net/ipv4/conf/eth0.100/rp_filter", "net./../kernel.shm_rmid_forced", "kernel.shm_rmid_forced"} {
		f.Add(key)
	}
	f.Fuzz(func(t *testing.T, key string) {
		dotted, err := sysctlKey(key)
		if err != nil {
			return
		}
		p := "/proc/sys/" + strings.ReplaceAll(dotted, ".", "/")
		if filepath.Clean(p) != p || strings.ContainsRune(p, 0) {
			t.Fatalf("key %q is accepted as %q, which names %q", key, dotted, p)
		}
	})
}

func TestValidateSysctl(t *testing.T) {
	for _, tc := range []struct {
		key                  string
		hostNetwork, hostIPC bool
		dotted               string
	}{
		{key: "net.ipv4.ip_forward", dotted: "net.ipv4.ip_forward"},
		{key: "kernel.shm_rmid_forced", dotted: "kernel.shm_rmid_forced"},
		{key: "kernel.sem", dotted: "kernel.sem"},
		{key: "fs.mqueue.msg_max", dotted: "fs.mqueue.msg_max"},
		{key: "kernel.msgmax", dotted: "kernel.msgmax"},
		{key: "kernel.shmmax", dotted: "kernel.shmmax"},
		{key: "kernel.domainname", dotted: "kernel.domainname"},
		{key: "kernel/shm_rmid_forced", dotted: "kernel.shm_rmid_forced"},
		// IPC sysctls are fine with the host network, net sysctls with the
		// host IPC namespace.
		{key: "kernel.shm_rmid_forced", hostNetwork: true, dotted: "kernel.shm_rmid_forced"},
		{key: "net.ipv4.ip_forward", hostIPC: true, dotted: "net.ipv4.ip_forward"},
	} {
		dotted, err := validateSysctl(tc.key, tc.hostNetwork, tc.hostIPC)
		require.NoError(t, err, "key %q", tc.key)
		assert.Equal(t, tc.dotted, dotted, "key %q", tc.key)
	}

	for _, tc := range []struct {
		key                  string
		hostNetwork, hostIPC bool
		wantErr              error
	}{
		{key: "net.ipv4.ip_forward", hostNetwork: true, wantErr: errdefs.ErrInvalidArgument},
		{key: "net/ipv4/ip_forward", hostNetwork: true, wantErr: errdefs.ErrInvalidArgument},
		// The UTS namespace is shared with the host along with the network one.
		{key: "kernel.domainname", hostNetwork: true, wantErr: errdefs.ErrInvalidArgument},
		// The pod hostname sets it; runc rejects the sysctl as conflicting.
		{key: "kernel.hostname", wantErr: errdefs.ErrInvalidArgument},
		{key: "kernel.shm_rmid_forced", hostIPC: true, wantErr: errdefs.ErrInvalidArgument},
		{key: "kernel/shm_rmid_forced", hostIPC: true, wantErr: errdefs.ErrInvalidArgument},
		{key: "kernel.msgmax", hostIPC: true, wantErr: errdefs.ErrInvalidArgument},
		{key: "fs.mqueue.msg_max", hostIPC: true, wantErr: errdefs.ErrInvalidArgument},
		// Only the IPC sysctls runc lists are accepted.
		{key: "kernel.sem_next_id", wantErr: errdefs.ErrInvalidArgument},
		{key: "kernel.shm_next_id", wantErr: errdefs.ErrInvalidArgument},
		{key: "kernel.msg_next_id", wantErr: errdefs.ErrInvalidArgument},
		{key: "net/ipv4/conf/eth0.100/rp_filter", wantErr: errdefs.ErrNotImplemented},
		{key: "user.max_user_namespaces", wantErr: errdefs.ErrNotImplemented},
		{key: "vm.swappiness", wantErr: errdefs.ErrInvalidArgument},
		{key: "net.\x00", wantErr: errdefs.ErrInvalidArgument},
	} {
		_, err := validateSysctl(tc.key, tc.hostNetwork, tc.hostIPC)
		assert.ErrorIs(t, err, tc.wantErr, "key %q, host network %v, host IPC %v", tc.key, tc.hostNetwork, tc.hostIPC)
	}
}

func TestSandboxSpec(t *testing.T) {
	pins := Pins{IPC: "/bundle/ns/ipc", UTS: "/bundle/ns/uts"}
	mounts := []specs.Mount{{Destination: "/etc/hosts", Type: "bind", Source: "/bundle/hosts", Options: []string{"rbind"}}}
	sysctls := map[string]string{"net.ipv4.ip_forward": "1"}
	annots := map[string]string{"io.kubernetes.cri.sandbox-id": testID}

	t.Run("pod namespaces", func(t *testing.T) {
		cfg := &Config{ID: testID, Hostname: "pod", NetNSPath: testNetNS, Sysctls: sysctls, Annotations: annots}
		spec := sandboxSpec(cfg, pins, mounts)
		assert.Equal(t, specs.Version, spec.Version)
		assert.Equal(t, "pod", spec.Hostname)
		assert.Equal(t, annots, spec.Annotations)
		assert.Equal(t, mounts, spec.Mounts)
		require.NotNil(t, spec.Linux)
		assert.Equal(t, sysctls, spec.Linux.Sysctl)
		assert.Equal(t, []specs.LinuxNamespace{
			{Type: specs.NetworkNamespace, Path: testNetNS},
			{Type: specs.IPCNamespace, Path: pins.IPC},
			{Type: specs.UTSNamespace, Path: pins.UTS},
			{Type: specs.PIDNamespace},
		}, spec.Linux.Namespaces)
	})

	t.Run("shared PID namespace", func(t *testing.T) {
		cfg := &Config{ID: testID, Hostname: "pod", NetNSPath: testNetNS, SharedPID: true}
		shared := pins
		shared.PID = "/proc/42/ns/pid"
		spec := sandboxSpec(cfg, shared, nil)
		assert.Equal(t, []specs.LinuxNamespace{
			{Type: specs.NetworkNamespace, Path: testNetNS},
			{Type: specs.IPCNamespace, Path: pins.IPC},
			{Type: specs.UTSNamespace, Path: pins.UTS},
			{Type: specs.PIDNamespace, Path: "/proc/42/ns/pid"},
		}, spec.Linux.Namespaces)
	})

	t.Run("host network keeps only the IPC namespace", func(t *testing.T) {
		cfg := &Config{ID: testID, Hostname: "node", HostNetwork: true}
		spec := sandboxSpec(cfg, Pins{IPC: pins.IPC}, nil)
		assert.Equal(t, []specs.LinuxNamespace{{Type: specs.IPCNamespace, Path: pins.IPC}, {Type: specs.PIDNamespace}}, spec.Linux.Namespaces)
	})

	t.Run("host ipc keeps the network and UTS namespaces", func(t *testing.T) {
		cfg := &Config{ID: testID, Hostname: "pod", HostIPC: true, NetNSPath: testNetNS}
		spec := sandboxSpec(cfg, Pins{UTS: pins.UTS}, nil)
		assert.Equal(t, []specs.LinuxNamespace{
			{Type: specs.NetworkNamespace, Path: testNetNS},
			{Type: specs.UTSNamespace, Path: pins.UTS},
			{Type: specs.PIDNamespace},
		}, spec.Linux.Namespaces)
	})

	t.Run("all host namespaces still carry a Linux section", func(t *testing.T) {
		cfg := &Config{ID: testID, Hostname: "node", HostNetwork: true, HostIPC: true, HostPID: true}
		spec := sandboxSpec(cfg, Pins{}, nil)
		require.NotNil(t, spec.Linux, "CRI tells a processless sandbox by the Linux section")
		assert.Empty(t, spec.Linux.Namespaces, "no PID namespace declaration for a host PID pod")
	})
}
