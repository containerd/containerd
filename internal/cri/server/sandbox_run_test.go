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
	"errors"
	"net"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/sandbox"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestToCNIPortMappings(t *testing.T) {
	for _, test := range []struct {
		desc            string
		criPortMappings []*runtime.PortMapping
		cniPortMappings []cni.PortMapping
	}{
		{
			desc: "empty CRI port mapping should map to empty CNI port mapping",
		},
		{
			desc: "CRI port mapping should be converted to CNI port mapping properly",
			criPortMappings: []*runtime.PortMapping{
				{
					Protocol:      runtime.Protocol_UDP,
					ContainerPort: 1234,
					HostPort:      5678,
					HostIp:        "123.124.125.126",
				},
				{
					Protocol:      runtime.Protocol_TCP,
					ContainerPort: 4321,
					HostPort:      8765,
					HostIp:        "126.125.124.123",
				},
				{
					Protocol:      runtime.Protocol_SCTP,
					ContainerPort: 1234,
					HostPort:      5678,
					HostIp:        "123.124.125.126",
				},
			},
			cniPortMappings: []cni.PortMapping{
				{
					HostPort:      5678,
					ContainerPort: 1234,
					Protocol:      "udp",
					HostIP:        "123.124.125.126",
				},
				{
					HostPort:      8765,
					ContainerPort: 4321,
					Protocol:      "tcp",
					HostIP:        "126.125.124.123",
				},
				{
					HostPort:      5678,
					ContainerPort: 1234,
					Protocol:      "sctp",
					HostIP:        "123.124.125.126",
				},
			},
		},
		{
			desc: "CRI port mapping without host port should be skipped",
			criPortMappings: []*runtime.PortMapping{
				{
					Protocol:      runtime.Protocol_UDP,
					ContainerPort: 1234,
					HostIp:        "123.124.125.126",
				},
				{
					Protocol:      runtime.Protocol_TCP,
					ContainerPort: 4321,
					HostPort:      8765,
					HostIp:        "126.125.124.123",
				},
			},
			cniPortMappings: []cni.PortMapping{
				{
					HostPort:      8765,
					ContainerPort: 4321,
					Protocol:      "tcp",
					HostIP:        "126.125.124.123",
				},
			},
		},
		{
			desc: "CRI port mapping with unsupported protocol should be skipped",
			criPortMappings: []*runtime.PortMapping{
				{
					Protocol:      runtime.Protocol_TCP,
					ContainerPort: 4321,
					HostPort:      8765,
					HostIp:        "126.125.124.123",
				},
			},
			cniPortMappings: []cni.PortMapping{
				{
					HostPort:      8765,
					ContainerPort: 4321,
					Protocol:      "tcp",
					HostIP:        "126.125.124.123",
				},
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.cniPortMappings, toCNIPortMappings(test.criPortMappings))
		})
	}
}

func TestSelectPodIP(t *testing.T) {
	for _, test := range []struct {
		desc                  string
		ips                   []string
		expectedIP            string
		expectedAdditionalIPs []string
		pref                  string
	}{
		{
			desc:                  "ipv4 should be picked even if ipv6 comes first",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43"},
			expectedIP:            "192.168.17.43",
			expectedAdditionalIPs: []string{"2001:db8:85a3::8a2e:370:7334"},
		},
		{
			desc:                  "ipv6 should be picked even if ipv4 comes first",
			ips:                   []string{"192.168.17.43", "2001:db8:85a3::8a2e:370:7334"},
			expectedIP:            "2001:db8:85a3::8a2e:370:7334",
			expectedAdditionalIPs: []string{"192.168.17.43"},
			pref:                  "ipv6",
		},
		{
			desc:                  "order should reflect ip selection",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43"},
			expectedIP:            "2001:db8:85a3::8a2e:370:7334",
			expectedAdditionalIPs: []string{"192.168.17.43"},
			pref:                  "cni",
		},
		{
			desc:                  "ipv4 should be picked when there is only ipv4",
			ips:                   []string{"192.168.17.43"},
			expectedIP:            "192.168.17.43",
			expectedAdditionalIPs: nil,
		},
		{
			desc:                  "ipv6 should be picked when there is no ipv4",
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334"},
			expectedIP:            "2001:db8:85a3::8a2e:370:7334",
			expectedAdditionalIPs: nil,
		},
		{
			desc:                  "the first ipv4 should be picked when there are multiple ipv4", // unlikely to happen
			ips:                   []string{"2001:db8:85a3::8a2e:370:7334", "192.168.17.43", "2001:db8:85a3::8a2e:370:7335", "192.168.17.45"},
			expectedIP:            "192.168.17.43",
			expectedAdditionalIPs: []string{"2001:db8:85a3::8a2e:370:7334", "2001:db8:85a3::8a2e:370:7335", "192.168.17.45"},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			var ipConfigs []*cni.IPConfig
			for _, ip := range test.ips {
				ipConfigs = append(ipConfigs, &cni.IPConfig{
					IP: net.ParseIP(ip),
				})
			}
			ip, additionalIPs := selectPodIPs(context.Background(), ipConfigs, test.pref)
			assert.Equal(t, test.expectedIP, ip)
			assert.Equal(t, test.expectedAdditionalIPs, additionalIPs)
		})
	}
}

func TestDisablePauseImagePullConfig(t *testing.T) {
	for _, test := range []struct {
		desc                  string
		sandboxer             string
		DisablePauseImagePull bool
	}{
		{
			desc:                  "Podsandbox sandboxer should pull pause image when DisablePauseImagePull is false",
			sandboxer:             "podsandbox",
			DisablePauseImagePull: false,
		},
		{
			desc:                  "Shim sandboxer should skip pause image pull when DisablePauseImagePull is true",
			sandboxer:             "shim",
			DisablePauseImagePull: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			cfg := criconfig.Config{
				RuntimeConfig: criconfig.RuntimeConfig{
					ContainerdConfig: criconfig.ContainerdConfig{
						DefaultRuntimeName: "test-runtime",
						Runtimes: map[string]criconfig.Runtime{
							"test-runtime": {
								Type:                  "io.containerd.runc.v2",
								Sandboxer:             test.sandboxer,
								DisablePauseImagePull: test.DisablePauseImagePull,
							},
						},
					},
				},
			}

			ociRuntime, err := cfg.GetSandboxRuntime(&runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{Name: "test", Namespace: "default"},
			}, "")
			require.NoError(t, err)
			assert.Equal(t, test.DisablePauseImagePull, ociRuntime.DisablePauseImagePull)
		})
	}
}

type mockSandboxService struct {
	fakeSandboxService
	startSandboxFunc    func(ctx context.Context, sandboxer string, sandboxID string) (sandbox.ControllerInstance, error)
	shutdownSandboxFunc func(ctx context.Context, sandboxer string, sandboxID string) error
}

func (m *mockSandboxService) StartSandbox(ctx context.Context, sandboxer string, sandboxID string) (sandbox.ControllerInstance, error) {
	if m.startSandboxFunc != nil {
		return m.startSandboxFunc(ctx, sandboxer, sandboxID)
	}
	return sandbox.ControllerInstance{}, nil
}

func (m *mockSandboxService) ShutdownSandbox(ctx context.Context, sandboxer string, sandboxID string) error {
	if m.shutdownSandboxFunc != nil {
		return m.shutdownSandboxFunc(ctx, sandboxer, sandboxID)
	}
	return nil
}

func (m *mockSandboxService) CreateSandbox(ctx context.Context, info sandbox.Sandbox, opts ...sandbox.CreateOpt) error {
	return nil
}

func (m *mockSandboxService) WaitSandbox(ctx context.Context, sandboxer string, sandboxID string) (<-chan containerd.ExitStatus, error) {
	ch := make(chan containerd.ExitStatus)
	return ch, nil
}

type fakeSandboxRunStore struct {
	sandbox.Store
	createdSandbox *sandbox.Sandbox
	deletedID      string
	updateErr      error
}

func (f *fakeSandboxRunStore) Create(ctx context.Context, sb sandbox.Sandbox) (sandbox.Sandbox, error) {
	f.createdSandbox = &sb
	return sb, nil
}

func (f *fakeSandboxRunStore) Delete(ctx context.Context, id string) error {
	f.deletedID = id
	return nil
}

func (f *fakeSandboxRunStore) Update(ctx context.Context, sb sandbox.Sandbox, fields ...string) (sandbox.Sandbox, error) {
	if f.updateErr != nil {
		return sandbox.Sandbox{}, f.updateErr
	}
	return sb, nil
}

type fakeLeasesManager struct {
	leases.Manager
}

func (f *fakeLeasesManager) Create(ctx context.Context, opts ...leases.Opt) (leases.Lease, error) {
	var l leases.Lease
	for _, opt := range opts {
		if err := opt(&l); err != nil {
			return leases.Lease{}, err
		}
	}
	return l, nil
}

func (f *fakeLeasesManager) Delete(ctx context.Context, l leases.Lease, opts ...leases.DeleteOpt) error {
	return nil
}

func TestRunPodSandboxRollback(t *testing.T) {
	t.Run("post-StartSandbox failure with ShutdownSandbox success", func(t *testing.T) {
		fakeStore := &fakeSandboxRunStore{
			updateErr: errors.New("fake update error"),
		}
		fakeLeases := &fakeLeasesManager{}

		shutdownCalled := false
		mockSB := &mockSandboxService{
			startSandboxFunc: func(ctx context.Context, sandboxer string, sandboxID string) (sandbox.ControllerInstance, error) {
				return sandbox.ControllerInstance{
					Pid: 1234,
				}, nil
			},
			shutdownSandboxFunc: func(ctx context.Context, sandboxer string, sandboxID string) error {
				shutdownCalled = true
				return nil
			},
		}

		c := newTestCRIService(func(service *criService) {
			runtimes := service.config.ContainerdConfig.Runtimes
			if runtimes == nil {
				runtimes = make(map[string]criconfig.Runtime)
			}
			rt := runtimes[service.config.ContainerdConfig.DefaultRuntimeName]
			rt.DisablePauseImagePull = true
			runtimes[service.config.ContainerdConfig.DefaultRuntimeName] = rt
			service.config.ContainerdConfig.Runtimes = runtimes

			client, _ := containerd.New("", containerd.WithServices(
				containerd.WithSandboxStore(fakeStore),
				containerd.WithLeasesService(fakeLeases),
			))
			service.client = client
			service.sandboxService = mockSB
		})

		req := &runtime.RunPodSandboxRequest{
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "test-sandbox",
					Namespace: "default",
					Uid:       "uid-123",
				},
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE, // Use host network to avoid netns setup
						},
					},
				},
				Windows: &runtime.WindowsPodSandboxConfig{
					SecurityContext: &runtime.WindowsSandboxSecurityContext{
						HostProcess: true,
					},
				},
			},
		}

		resp, err := c.RunPodSandbox(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.True(t, shutdownCalled, "ShutdownSandbox should be called during rollback")

		// Because ShutdownSandbox returned success (nil), metadata cleanup should succeed
		// and the sandbox should NOT be in the memory store.
		assert.NotEmpty(t, fakeStore.deletedID, "Sandbox metadata should be deleted from store")
		_, err = c.sandboxStore.Get(fakeStore.deletedID)
		assert.Error(t, err, "Sandbox should not be added to memory store on successful rollback")
	})

	t.Run("post-StartSandbox failure with ShutdownSandbox failure", func(t *testing.T) {
		fakeStore := &fakeSandboxRunStore{
			updateErr: errors.New("fake update error"),
		}
		fakeLeases := &fakeLeasesManager{}

		shutdownCalled := false
		mockSB := &mockSandboxService{
			startSandboxFunc: func(ctx context.Context, sandboxer string, sandboxID string) (sandbox.ControllerInstance, error) {
				return sandbox.ControllerInstance{
					Pid: 1234,
				}, nil
			},
			shutdownSandboxFunc: func(ctx context.Context, sandboxer string, sandboxID string) error {
				shutdownCalled = true
				return errors.New("fake shutdown error")
			},
		}

		c := newTestCRIService(func(service *criService) {
			runtimes := service.config.ContainerdConfig.Runtimes
			if runtimes == nil {
				runtimes = make(map[string]criconfig.Runtime)
			}
			rt := runtimes[service.config.ContainerdConfig.DefaultRuntimeName]
			rt.DisablePauseImagePull = true
			runtimes[service.config.ContainerdConfig.DefaultRuntimeName] = rt
			service.config.ContainerdConfig.Runtimes = runtimes

			client, _ := containerd.New("", containerd.WithServices(
				containerd.WithSandboxStore(fakeStore),
				containerd.WithLeasesService(fakeLeases),
			))
			service.client = client
			service.sandboxService = mockSB
		})

		req := &runtime.RunPodSandboxRequest{
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{
					Name:      "test-sandbox",
					Namespace: "default",
					Uid:       "uid-123",
				},
				Linux: &runtime.LinuxPodSandboxConfig{
					SecurityContext: &runtime.LinuxSandboxSecurityContext{
						NamespaceOptions: &runtime.NamespaceOption{
							Network: runtime.NamespaceMode_NODE, // Use host network to avoid netns setup
						},
					},
				},
				Windows: &runtime.WindowsPodSandboxConfig{
					SecurityContext: &runtime.WindowsSandboxSecurityContext{
						HostProcess: true,
					},
				},
			},
		}

		resp, err := c.RunPodSandbox(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.True(t, shutdownCalled, "ShutdownSandbox should be called during rollback")

		// Because ShutdownSandbox failed, the sandbox metadata should NOT be deleted from store
		// and it should be added to the memory store in StateUnknown state.
		assert.Empty(t, fakeStore.deletedID, "Sandbox metadata should not be deleted from store when shutdown fails")

		// Check that the sandbox is now in the memory store
		sbList := c.sandboxStore.List()
		assert.Len(t, sbList, 1, "Sandbox should be added to memory store on failed rollback")
		if len(sbList) > 0 {
			assert.Equal(t, sandboxstore.StateUnknown, sbList[0].Status.Get().State)
		}
	})
}
