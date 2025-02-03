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

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/containerd/nri/pkg/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// Test NRI networking capabilities.
func TestNriPluginNetworkingSynchronization(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins get pod networking attributes on synchronization.")

	var (
		tc = &nriTest{
			t: t,
		}
		podCount  = 3
		ctrPerPod = 2
		podPrefix = "pod-net-sync"
	)

	tc.setup()

	for i := 0; i < podCount; i++ {
		podID := tc.runPod(fmt.Sprintf("%s%d", podPrefix, i))
		for j := 0; j < ctrPerPod; j++ {
			tc.startContainer(podID, fmt.Sprintf("ctr%d", j))
		}
	}

	numPods := 0
	// override hooks are executed after the sync events, use a channel to synchronize the test
	syncCh := make(chan struct{})
	plugin := &mockPlugin{
		synchronize: func(mp *mockPlugin, pods []*api.PodSandbox, c []*api.Container) ([]*api.ContainerUpdate, error) {
			defer close(syncCh)
			for _, pod := range pods {
				t.Logf("synchronize pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), getNetworkNamespace(pod), pod.GetIps())
				// only process the pods created by this test
				if !strings.HasPrefix(pod.GetName(), podPrefix) {
					t.Logf("DEBUG pod namespace %s name %s tc.namespace %s", pod.GetNamespace(), pod.GetName(), tc.namespace)
					continue
				}
				numPods++
				// get the pod network namespace
				ns := getNetworkNamespace(pod)
				// test only creates pods with network
				require.NotEmpty(t, ns)
				require.ElementsMatch(t, pod.GetIps(), getNetworkNamespaceIPs(ns))
			}
			return nil, nil
		},
	}

	tc.connectNewPlugin(plugin)

	err := plugin.Wait(PluginSynchronized, time.After(pluginSyncTimeout))
	require.NoError(t, err, "plugin sync wait")

	for _, id := range tc.pods {
		for _, plugin := range tc.plugins {
			_, ok := plugin.pods[id]
			require.True(tc.t, ok, "runtime sync of pod "+id)
		}
	}

	for _, id := range tc.ctrs {
		for _, plugin := range tc.plugins {
			_, ok := plugin.ctrs[id]
			require.True(t, ok, "runtime sync of container "+id)
		}
	}

	select {
	case <-syncCh:
	case <-time.After(pluginSyncTimeout):
		t.Fatalf("test timed out waiting for the plugin to sync")
	}
	require.Equal(t, numPods, podCount)
}

func TestNriPluginNetworkingLifecycle(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins get pod networking attributes during the pod lifecycle.")

	var (
		tc = &nriTest{
			t: t,
		}
		podName = "pod-net-lifecycle"
	)

	tc.setup()

	hookExecs := 0
	// override hooks are executed after the sync events, use channels to synchronize the test
	runPodSandboxCh := make(chan struct{})
	stopPodSandboxCh := make(chan struct{})
	removePodSandboxCh := make(chan struct{})

	plugin := &mockPlugin{
		runPodSandbox: func(mp *mockPlugin, pod *api.PodSandbox) error {
			t.Logf("runPodSandbox pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), getNetworkNamespace(pod), pod.GetIps())
			if pod.Name != podName {
				return nil
			}
			defer close(runPodSandboxCh)
			// get the pod network namespace
			ns := getNetworkNamespace(pod)
			// test only creates pods with network
			require.NotEmpty(t, ns)
			require.ElementsMatch(t, pod.GetIps(), getNetworkNamespaceIPs(ns))
			hookExecs++
			return nil
		},
		stopPodSandbox: func(mp *mockPlugin, pod *api.PodSandbox) error {
			t.Logf("stopPodSandbox pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), getNetworkNamespace(pod), pod.GetIps())
			if pod.Name != podName {
				return nil
			}
			defer close(stopPodSandboxCh)
			// get the pod network namespace
			ns := getNetworkNamespace(pod)
			// test only creates pods with network
			require.NotEmpty(t, ns)
			require.ElementsMatch(t, pod.GetIps(), getNetworkNamespaceIPs(ns))
			hookExecs++
			return nil
		},
		removePodSandbox: func(mp *mockPlugin, pod *api.PodSandbox) error {
			t.Logf("removePodSandbox pod %s/%s: namespace=%s ips=%v", pod.GetNamespace(), pod.GetName(), getNetworkNamespace(pod), pod.GetIps())
			if pod.Name != podName {
				return nil
			}
			defer close(removePodSandboxCh)
			// get the pod network namespace
			ns := getNetworkNamespace(pod)
			// test only creates pods with network but at this point networking namespace is not present
			require.Empty(t, ns)
			// the Pod assigned IPs are still available
			require.NotEmpty(t, pod.GetIps())
			hookExecs++
			return nil
		},
	}

	tc.connectNewPlugin(plugin)

	err := plugin.Wait(PluginSynchronized, time.After(pluginSyncTimeout))
	require.NoError(t, err, "plugin sync wait")

	sbConfig := PodSandboxConfig(podName, tc.namespace)
	podID, err := runtimeService.RunPodSandbox(sbConfig, *runtimeHandler)
	require.NoError(t, err)

	err = plugin.Wait(&Event{Pod: podID, Type: EventType(RunPodSandbox)}, time.After(pluginSyncTimeout))
	require.NoError(t, err, "plugin sync wait")
	select {
	case <-runPodSandboxCh:
	case <-time.After(pluginSyncTimeout):
		t.Fatalf("test timed out waiting for the RunPodSandbox hook to be executed")
	}

	assert.NoError(t, runtimeService.StopPodSandbox(podID))
	err = plugin.Wait(&Event{Pod: podID, Type: EventType(StopPodSandbox)}, time.After(pluginSyncTimeout))
	require.NoError(t, err, "plugin sync wait")
	select {
	case <-stopPodSandboxCh:
	case <-time.After(pluginSyncTimeout):
		t.Fatalf("test timed out waiting for the StopPodSandbox hook to be executed")
	}

	assert.NoError(t, runtimeService.RemovePodSandbox(podID))
	err = plugin.Wait(&Event{Pod: podID, Type: EventType(RemovePodSandbox)}, time.After(pluginSyncTimeout))
	require.NoError(t, err, "plugin sync wait")
	select {
	case <-removePodSandboxCh:
	case <-time.After(pluginSyncTimeout):
		t.Fatalf("test timed out waiting for the RemovePodSandbox hook to be executed")
	}

	if hookExecs != 3 {
		t.Fatalf("expected 3 hooks executed only got %d", hookExecs)
	}

}

func getNetworkNamespace(pod *api.PodSandbox) string {
	// get the pod network namespace
	for _, namespace := range pod.Linux.GetNamespaces() {
		if namespace.Type == "network" {
			return namespace.Path
		}
	}
	return ""
}

func getNetworkNamespaceIPs(nsPath string) []string {
	ips := []string{}
	sandboxNs, err := netns.GetFromPath(nsPath)
	if err != nil {
		return ips
	}
	// to avoid golang problem with goroutines we create the socket in the
	// namespace and use it directly
	nhNs, err := netlink.NewHandleAt(sandboxNs)
	if err != nil {
		return ips
	}

	// there is a convention the interface inside the Pod is always named eth0
	// internal/cri/server/helpers.go: defaultIfName = "eth0"
	nsLink, err := nhNs.LinkByName("eth0")
	if err != nil {
		return ips
	}
	addrs, err := nhNs.AddrList(nsLink, netlink.FAMILY_ALL)
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		// ignore link local and loopback addresses
		// those are not added by the CNI
		if addr.IP.IsGlobalUnicast() {
			ips = append(ips, addr.IP.String())
		}
	}
	return ips
}
