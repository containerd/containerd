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
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/opencontainers/selinux/go-selinux"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/containerd/v2/integration/remote"
)

const (
	nriTestSocket        = "/var/run/nri-test.sock"
	pluginSyncTimeout    = 3 * time.Second
	containerStopTimeout = 10
	onlineCpus           = "/sys/devices/system/cpu/online"
	normalMems           = "/sys/devices/system/node/has_normal_memory"
)

var (
	availableCpuset []string
	availableMemset []string
)

// skipNriTestIfNecessary skips NRI tests if necessary.
func skipNriTestIfNecessary(t *testing.T, extraSkipChecks ...map[string]bool) {
	if goruntime.GOOS != "linux" {
		t.Skip("Not running on linux")
	}

	if selinux.GetEnabled() {
		// https://github.com/containerd/containerd/pull/7892#issuecomment-1369825603
		t.Skip("SELinux relabeling is not supported for NRI yet")
	}
	_, err := os.Stat(nriTestSocket)
	if err != nil {
		t.Skip("Containerd test instance does not have NRI enabled")
	}

	for _, checks := range extraSkipChecks {
		for check, skip := range checks {
			if skip {
				t.Skip(check)
			}
		}
	}
}

// Test successful Nri plugin setup.
func TestNriPluginSetup(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins can connect and get set up.")

	var (
		tc = &nriTest{
			t: t,
			plugins: []*mockPlugin{
				{},
				{},
				{},
			},
		}
	)

	tc.setup()
}

// Test that NRI request timeouts follow the plugin's failure policy.
func TestNriPluginFailurePolicy(t *testing.T) {
	skipNriTestIfNecessary(t)

	for _, tc := range []struct {
		name         string
		policy       *api.FailurePolicy
		wantRunError bool
		wantKept     bool
	}{
		{
			name: "default policy drops plugin and proceeds",
		},
		{
			name:         "fail request and drop plugin",
			policy:       &api.FailurePolicy{FailRequest: true},
			wantRunError: true,
		},
		{
			name:     "keep plugin and proceed",
			policy:   &api.FailurePolicy{KeepConnected: true},
			wantKept: true,
		},
		{
			name:         "fail request and keep plugin connected",
			policy:       &api.FailurePolicy{FailRequest: true, KeepConnected: true},
			wantRunError: true,
			wantKept:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				timedOut = make(chan struct{})
				healthy  = make(chan struct{})
				once     sync.Once
				plugin   = &mockPlugin{
					runPodSandbox: func(mp *mockPlugin, pod *api.PodSandbox) error {
						switch pod.Name {
						case "timeout-target":
							once.Do(func() {
								time.Sleep(mp.stub.RequestTimeout() + 250*time.Millisecond)
								close(timedOut)
							})
						case "healthy-target":
							close(healthy)
						}
						return nil
					},
				}
				nt = &nriTest{t: t, plugins: []*mockPlugin{plugin}}
			)

			if tc.policy != nil {
				plugin.opts = append(plugin.opts, stub.WithFailurePolicy(tc.policy))
			}
			nt.setup()
			t.Cleanup(plugin.Stop)

			runPod := func(name string) (string, error) {
				return nt.runtime.RunPodSandbox(PodSandboxConfig(name, nt.namespace), *runtimeHandler)
			}
			cleanupPod := func(id string) {
				t.Cleanup(func() {
					assert.NoError(t, nt.runtime.StopPodSandbox(id))
					assert.NoError(t, nt.runtime.RemovePodSandbox(id))
				})
			}

			id, err := runPod("timeout-target")
			if id != "" {
				cleanupPod(id)
			}
			if tc.wantRunError {
				require.Error(t, err, "timed-out NRI hook should fail the pod request")
			} else {
				require.NoError(t, err, "policy should allow the pod request")
			}

			select {
			case <-timedOut:
			case <-time.After(plugin.stub.RequestTimeout() + pluginSyncTimeout):
				t.Fatal("timed-out plugin callback did not return")
			}

			if tc.wantKept {
				id, err = runPod("healthy-target")
				require.NoError(t, err, "kept-connected plugin should handle the next pod")
				cleanupPod(id)
				select {
				case <-healthy:
				case <-time.After(pluginSyncTimeout):
					t.Fatal("kept-connected plugin did not receive the next request")
				}
			} else {
				select {
				case <-plugin.closedCh:
				case <-time.After(pluginSyncTimeout):
					t.Fatal("policy should disconnect a timed-out plugin")
				}
			}
		})
	}
}

// Test that an NRI plugin which disconnects while idle does not fail the next
// CRI lifecycle request or prevent later plugins from receiving it.
func TestNriPluginIdleDisconnect(t *testing.T) {
	skipNriTestIfNecessary(t)

	var (
		nextPodEvent = make(chan struct{}, 1)
		failing      = &mockPlugin{
			name: "idle-disconnect-plugin",
			opts: []stub.Option{stub.WithFailurePolicy(&api.FailurePolicy{FailRequest: true, KeepConnected: true})},
		}
		observer = &mockPlugin{
			name: "observer-plugin",
			runPodSandbox: func(_ *mockPlugin, pod *api.PodSandbox) error {
				if pod.Name == "after-disconnect" {
					nextPodEvent <- struct{}{}
				}
				return nil
			},
		}
		nt = &nriTest{t: t, plugins: []*mockPlugin{failing, observer}}
	)

	nt.setup()
	t.Cleanup(failing.Stop)
	t.Cleanup(observer.Stop)

	nt.runPod("before-disconnect")

	failing.Stop()
	select {
	case <-failing.closedCh:
	case <-time.After(pluginSyncTimeout):
		t.Fatal("disconnected plugin did not stop")
	}

	// Allow the runtime-side ttrpc connection watcher to observe the idle
	// disconnect before sending the next request.
	time.Sleep(250 * time.Millisecond)

	nt.runPod("after-disconnect")
	select {
	case <-nextPodEvent:
	case <-time.After(pluginSyncTimeout):
		t.Fatal("later plugin did not receive the request after idle disconnect")
	}
}

// Test that fail-request timeouts stop NRI fanout during pod teardown, while
// CRI continues the teardown operation without retrying the notification.
func TestNriPluginFailurePolicyPodTeardown(t *testing.T) {
	skipNriTestIfNecessary(t)

	for _, target := range []string{StopPodSandbox, RemovePodSandbox} {
		t.Run(target, func(t *testing.T) {
			var (
				timedOut = make(chan struct{})
				notified = make(chan string, 4)
				once     sync.Once
				failing  = &mockPlugin{
					name: "timeout-plugin",
					opts: []stub.Option{stub.WithFailurePolicy(&api.FailurePolicy{FailRequest: true, KeepConnected: true})},
				}
				observer = &mockPlugin{
					name: "observer-plugin",
					stopPodSandbox: func(_ *mockPlugin, pod *api.PodSandbox) error {
						notified <- StopPodSandbox
						return nil
					},
					removePodSandbox: func(_ *mockPlugin, pod *api.PodSandbox) error {
						notified <- RemovePodSandbox
						return nil
					},
				}
				nt = &nriTest{t: t, plugins: []*mockPlugin{failing, observer}}
			)

			block := func(event string) {
				if event != target {
					return
				}
				once.Do(func() {
					time.Sleep(failing.stub.RequestTimeout() + 250*time.Millisecond)
					close(timedOut)
				})
			}
			failing.stopPodSandbox = func(_ *mockPlugin, _ *api.PodSandbox) error {
				block(StopPodSandbox)
				return nil
			}
			failing.removePodSandbox = func(_ *mockPlugin, _ *api.PodSandbox) error {
				block(RemovePodSandbox)
				return nil
			}

			nt.setup()
			t.Cleanup(failing.Stop)
			t.Cleanup(observer.Stop)

			id, err := nt.runtime.RunPodSandbox(PodSandboxConfig("pod0", nt.namespace), *runtimeHandler)
			require.NoError(t, err, "run pod sandbox")
			removed := false
			t.Cleanup(func() {
				if !removed {
					assert.NoError(t, nt.runtime.StopPodSandbox(id))
					assert.NoError(t, nt.runtime.RemovePodSandbox(id))
				}
			})

			if target == RemovePodSandbox {
				require.NoError(t, nt.runtime.StopPodSandbox(id), "stop pod sandbox before removal")
				select {
				case event := <-notified:
					require.Equal(t, StopPodSandbox, event, "observer should receive successful stop notification")
				case <-time.After(pluginSyncTimeout):
					t.Fatal("observer did not receive stop notification")
				}
			}

			switch target {
			case StopPodSandbox:
				require.NoError(t, nt.runtime.StopPodSandbox(id), "CRI should continue after NRI stop timeout")
			case RemovePodSandbox:
				err = nt.runtime.RemovePodSandbox(id)
				require.NoError(t, err, "CRI should continue after NRI remove timeout")
				removed = true
			}

			select {
			case <-timedOut:
			case <-time.After(failing.stub.RequestTimeout() + pluginSyncTimeout):
				t.Fatal("timed-out plugin callback did not return")
			}

			select {
			case event := <-notified:
				t.Fatalf("later plugin unexpectedly received %s after %s failure", event, target)
			case <-time.After(250 * time.Millisecond):
			}

			if target == StopPodSandbox {
				require.NoError(t, nt.runtime.RemovePodSandbox(id), "remove stopped pod sandbox")
				removed = true
			}
		})
	}
}

// Test NRI runtime/plugin state synchronization.
func TestNriPluginSynchronization(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins get properly synchronized with the runtime state.")

	var (
		tc = &nriTest{
			t: t,
		}
		podCount  = 3
		ctrPerPod = 2
	)

	tc.setup()

	for i := range podCount {
		podID := tc.runPod(fmt.Sprintf("pod%d", i))
		for j := range ctrPerPod {
			tc.startContainer(podID, fmt.Sprintf("ctr%d", j))
		}
	}

	for _, plugin := range []*mockPlugin{{}, {}, {}} {
		tc.connectNewPlugin(plugin)
	}

	for _, plugin := range tc.plugins {
		err := plugin.Wait(PluginSynchronized, time.After(pluginSyncTimeout))
		require.NoError(t, err, "plugin sync wait")
	}

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
}

// Test mount injection into containers by NRI plugins.
func TestNriMountInjection(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins can inject mounts into containers.")

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		injectMount = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			adjust := &api.ContainerAdjustment{}
			adjust.AddMount(&api.Mount{
				Destination: "/out",
				Source:      out,
				Type:        "bind",
				Options:     []string{"bind"},
			})
			return adjust, nil, nil
		}
	)

	tc.plugins[0].createContainer = injectMount
	tc.setup()

	msg := fmt.Sprintf("hello process %d", os.Getpid())
	cmd := "echo " + msg + " > /out/result; sleep 3600"

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c", cmd),
	)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")
	require.Equal(t, msg+"\n", string(chk), "check result")
}

// Test environment variable injection by NRI plugins.
func TestNriEnvironmentInjection(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins can inject environment variables into containers.")

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		injectEnv = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			adjust := &api.ContainerAdjustment{}
			adjust.AddMount(&api.Mount{
				Destination: "/out",
				Source:      out,
				Type:        "bind",
				Options:     []string{"bind"},
			})
			adjust.AddEnv("TEST_ENV_NAME", "TEST_ENV_VALUE")
			return adjust, nil, nil
		}
	)

	tc.plugins[0].createContainer = injectEnv
	tc.setup()

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c", "echo $TEST_ENV_NAME > /out/result; sleep 3600"),
	)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")
	require.Equal(t, "TEST_ENV_VALUE\n", string(chk), "check result")
}

// Test annotation injection by NRI plugins.
func TestNriAnnotationInjection(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins can inject annotations into containers.")

	var (
		key   = "TEST_ANNOTATION_KEY"
		value = "TEST_ANNOTATION_VALUE"
		tc    = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		injectAnnotation = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			adjust := &api.ContainerAdjustment{}
			adjust.AddAnnotation(key, value)
			return adjust, nil, nil
		}
	)

	tc.plugins[0].createContainer = injectAnnotation
	tc.setup()

	podID := tc.runPod("pod0")
	id := tc.startContainer(podID, "ctr0")

	timeout := time.After(pluginSyncTimeout)
	err := tc.plugins[0].Wait(ContainerEvent(tc.plugins[0].ctrs[id], PostCreateContainer), timeout)
	require.NoError(t, err, "wait for container post-create event")
	require.Equal(t, value, tc.plugins[0].ctrs[id].Annotations[key], "updated annotations")
}

// Test linux device injection by NRI plugins.
func TestNriLinuxDeviceInjection(t *testing.T) {
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins can inject linux devices into containers.")

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		injectDev = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			adjust := &api.ContainerAdjustment{}
			adjust.AddMount(&api.Mount{
				Destination: "/out",
				Source:      out,
				Type:        "bind",
				Options:     []string{"bind"},
			})
			adjust.AddDevice(&api.LinuxDevice{
				Path:     "/dev/pie",
				Type:     "c",
				Major:    31,
				Minor:    41,
				Uid:      api.UInt32(uint32(11)),
				Gid:      api.UInt32(uint32(22)),
				FileMode: api.FileMode(uint32(0664)),
			})
			return adjust, nil, nil
		}
	)

	tc.plugins[0].createContainer = injectDev
	tc.setup()

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c", "stat -c %F-%a-%u:%g-%t:%T /dev/pie > /out/result; sleep 3600"),
	)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")
	require.Equal(t, "character special file-664-11:22-1f:29\n", string(chk), "check result")
}

// Test linux cpuset adjustment by NRI plugins.
func TestNriLinuxCpusetAdjustment(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc does not propagate NRI cpuset adjustments to the container's /proc/self/status")
	}
	skipNriTestIfNecessary(t,
		map[string]bool{
			"not enough online CPUs for test": len(getAvailableCpuset(t)) < 2,
		},
	)

	t.Log("Test that NRI plugins can adjust linux cpusets of containers.")

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		adjustCpuset = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			adjust := &api.ContainerAdjustment{}
			adjust.AddMount(&api.Mount{
				Destination: "/out",
				Source:      out,
				Type:        "bind",
				Options:     []string{"bind"},
			})
			adjust.SetLinuxCPUSetCPUs(availableCpuset[1])
			return adjust, nil, nil
		}
	)

	tc.plugins[0].createContainer = adjustCpuset
	tc.setup()

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c", "grep Cpus_allowed_list: /proc/self/status > /out/result; "+
			"sleep 3600"),
	)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")

	expected := "Cpus_allowed_list:\t" + availableCpuset[1] + "\n"
	require.Equal(t, expected, string(chk), "check result")
}

// Test linux memset adjustment by NRI plugins.
func TestNriLinuxMemsetAdjustment(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc does not propagate NRI memset adjustments to the container's /proc/self/status")
	}
	skipNriTestIfNecessary(t,
		map[string]bool{
			"not enough online memory nodes for test": len(getAvailableMemset(t)) < 2,
		},
	)

	t.Log("Test that NRI plugins can adjust linux memsets of containers.")

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		adjustMemset = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			adjust := &api.ContainerAdjustment{}
			adjust.AddMount(&api.Mount{
				Destination: "/out",
				Source:      out,
				Type:        "bind",
				Options:     []string{"bind"},
			})
			adjust.SetLinuxCPUSetMems(availableMemset[1])
			return adjust, nil, nil
		}
	)

	tc.plugins[0].createContainer = adjustMemset
	tc.setup()

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c", "grep Mems_allowed_list: /proc/self/status > /out/result; "+
			"sleep 3600"),
	)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")

	expected := "Mems_allowed_list:\t" + availableMemset[1] + "\n"
	require.Equal(t, expected, string(chk), "check result")
}

// Test creation-time linux cpuset update of existing containers by NRI plugins.
func TestNriLinuxCpusetAdjustmentUpdate(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc does not propagate NRI cpuset adjustments to the container's /proc/self/status")
	}
	skipNriTestIfNecessary(t,
		map[string]bool{
			"not enough online CPUs for test": len(getAvailableCpuset(t)) < 2,
		},
	)

	t.Log("Test that NRI plugins can update linux cpusets of existing containers.")
	t.Logf("availableCpuset values is %v", availableCpuset)

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		ctr0         string
		updateCpuset = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			var (
				adjust *api.ContainerAdjustment
				update []*api.ContainerUpdate
			)
			if ctr.Name == "ctr0" {
				ctr0 = ctr.Id
				adjust = &api.ContainerAdjustment{}
				adjust.AddMount(&api.Mount{
					Destination: "/out",
					Source:      out,
					Type:        "bind",
					Options:     []string{"bind"},
				})
				t.Logf("ctr0 availableCpuset values is %v", availableCpuset)
				adjust.SetLinuxCPUSetCPUs(availableCpuset[0])
			} else {
				update = []*api.ContainerUpdate{{}}
				update[0].SetContainerId(ctr0)
				t.Logf("ctr1 availableCpuset values is %v", availableCpuset)
				update[0].SetLinuxCPUSetCPUs(availableCpuset[1])
			}
			return adjust, update, nil
		}
	)

	tc.plugins[0].createContainer = updateCpuset
	tc.setup()

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c",
			`prev=""
             while true; do
                 cpus="$(grep Cpus_allowed_list: /proc/self/status)"
                 [ "$cpus" != "$prev" ] && echo "$cpus" > /out/result
                 cpus="$prev"
                 sleep 0.2
             done`),
	)
	tc.startContainer(podID, "ctr1",
		WithCommand("/bin/sh", "-c", "sleep 3600"),
	)

	time.Sleep(1 * time.Second)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")

	expected := "Cpus_allowed_list:\t" + availableCpuset[1] + "\n"
	require.Equal(t, expected, string(chk), "check result")
}

// Test creation-time linux memset update of existing containers by NRI plugins.
func TestNriLinuxMemsetAdjustmentUpdate(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc does not propagate NRI memset adjustments to the container's /proc/self/status")
	}
	skipNriTestIfNecessary(t,
		map[string]bool{
			"not enough online memory nodes for test": len(getAvailableMemset(t)) < 2,
		},
	)

	t.Log("Test that NRI plugins can update linux memsets of existing containers.")

	var (
		out = t.TempDir()
		tc  = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		ctr0         string
		updateMemset = func(p *mockPlugin, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
			var (
				adjust *api.ContainerAdjustment
				update []*api.ContainerUpdate
			)
			if ctr.Name == "ctr0" {
				ctr0 = ctr.Id
				adjust = &api.ContainerAdjustment{}
				adjust.AddMount(&api.Mount{
					Destination: "/out",
					Source:      out,
					Type:        "bind",
					Options:     []string{"bind"},
				})
				adjust.SetLinuxCPUSetMems(availableMemset[0])
			} else {
				update = []*api.ContainerUpdate{{}}
				update[0].SetContainerId(ctr0)
				update[0].SetLinuxCPUSetMems(availableMemset[1])
			}
			return adjust, update, nil
		}
	)

	tc.plugins[0].createContainer = updateMemset
	tc.setup()

	podID := tc.runPod("pod0")
	tc.startContainer(podID, "ctr0",
		WithCommand("/bin/sh", "-c",
			`prev=""
             while true; do
                 mems="$(grep Mems_allowed_list: /proc/self/status)"
                 [ "$mems" != "$prev" ] && echo "$mems" > /out/result
                 mems="$prev"
                 sleep 0.2
             done`),
	)
	tc.startContainer(podID, "ctr1",
		WithCommand("/bin/sh", "-c", "sleep 3600"),
	)

	time.Sleep(1 * time.Second)

	chk, err := waitForFileAndRead(filepath.Join(out, "result"), time.Second)
	require.NoError(t, err, "read result")

	expected := "Mems_allowed_list:\t" + availableMemset[1] + "\n"
	require.Equal(t, expected, string(chk), "check result")
}

// Test pod sandbox updates received by NRI plugins.
func TestNriUpdatePodSandbox(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc does not implement cgroup memory limits")
	}
	skipNriTestIfNecessary(t)

	t.Log("Test that NRI plugins receive pod sandbox updates.")

	var (
		tc = &nriTest{
			t:       t,
			plugins: []*mockPlugin{{}},
		}
		overheadMemLimit int64
		resMemLimit      int64
		updateMemLimit   = func(mp *mockPlugin, pod *api.PodSandbox, overhead *api.LinuxResources, res *api.LinuxResources) error {
			overheadMemLimit = overhead.GetMemory().GetLimit().GetValue()
			resMemLimit = res.GetMemory().GetLimit().GetValue()
			return nil
		}
	)

	tc.plugins[0].updatePodSandbox = updateMemLimit
	tc.setup()

	podID := tc.runPod("pod0")
	_ = tc.startContainer(podID, "ctr0")

	overhead := &runtime.LinuxContainerResources{MemoryLimitInBytes: 10}
	res := &runtime.LinuxContainerResources{MemoryLimitInBytes: 5}
	err := tc.runtime.UpdatePodSandboxResources(podID, overhead, res)
	assert.NoError(tc.t, err, "update pod sandbox")

	timeout := time.After(pluginSyncTimeout)
	err = tc.plugins[0].Wait(PodSandboxEvent(tc.plugins[0].pods[podID], UpdatePodSandbox), timeout)
	assert.NoError(tc.t, err, "wait for update pod sandbox")

	err = tc.plugins[0].Wait(PodSandboxEvent(tc.plugins[0].pods[podID], PostUpdatePodSandbox), timeout)
	assert.NoError(tc.t, err, "wait for post update pod sandbox")

	assert.Equal(t, overheadMemLimit, overhead.MemoryLimitInBytes)
	assert.Equal(t, resMemLimit, res.MemoryLimitInBytes)
}

// Test pod sandbox resource updates persist across containerd restarts.
func TestUpdatePodSandboxWithRestart(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc sandboxes do not survive containerd restart; state recovery is not supported")
	}
	skipNriTestIfNecessary(t)

	t.Log("Test that pod sandbox resource updates persist across containerd restarts.")

	tc := &nriTest{
		t: t,
	}
	tc.setup()

	podID := tc.runPod("pod0")
	_ = tc.startContainer(podID, "ctr0")

	overhead := &runtime.LinuxContainerResources{MemoryLimitInBytes: 10}
	res := &runtime.LinuxContainerResources{MemoryLimitInBytes: 5}
	err := tc.runtime.UpdatePodSandboxResources(podID, overhead, res)
	assert.NoError(tc.t, err, "update pod sandbox")

	t.Logf("Verify sandbox resources are updated before restart")
	_, info, err := SandboxInfo(podID)
	require.NoError(t, err)
	require.NotNil(t, info.Resources)
	require.NotNil(t, info.Overhead)
	assert.Equal(t, res.MemoryLimitInBytes, info.Resources.GetLinux().GetMemoryLimitInBytes())
	assert.Equal(t, overhead.MemoryLimitInBytes, info.Overhead.GetLinux().GetMemoryLimitInBytes())

	t.Logf("Restart containerd")
	RestartContainerd(t, syscall.SIGTERM)

	t.Logf("Verify sandbox resources are still updated after restart")
	_, info, err = SandboxInfo(podID)
	require.NoError(t, err)
	require.NotNil(t, info.Resources)
	require.NotNil(t, info.Overhead)
	assert.Equal(t, res.MemoryLimitInBytes, info.Resources.GetLinux().GetMemoryLimitInBytes())
	assert.Equal(t, overhead.MemoryLimitInBytes, info.Overhead.GetLinux().GetMemoryLimitInBytes())
}

// Test NRI vs. containerd restart.
func TestNriPluginContainerdRestart(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc sandboxes do not survive containerd restart; state recovery is not supported")
	}
	skipNriTestIfNecessary(t)

	t.Log("Test NRI plugins vs. containerd restart.")

	var (
		tc = &nriTest{
			t: t,
		}
		podCount  = 3
		ctrPerPod = 2
	)

	tc.setup()

	for i := range podCount {
		podID := tc.runPod(fmt.Sprintf("pod%d", i))
		for j := range ctrPerPod {
			tc.startContainer(podID, fmt.Sprintf("ctr%d", j))
		}
	}

	for _, plugin := range []*mockPlugin{{}, {}, {}} {
		tc.connectNewPlugin(plugin)
	}

	for _, plugin := range tc.plugins {
		err := plugin.Wait(PluginSynchronized, time.After(pluginSyncTimeout))
		require.NoError(t, err, "plugin sync wait")
	}

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

	t.Logf("Restart containerd")
	RestartContainerd(t, syscall.SIGTERM)

	for _, plugin := range tc.plugins {
		require.True(t, plugin.closed, "plugin connection closed")
	}
}

// An NRI test along with its state.
type nriTest struct {
	t         *testing.T
	name      string
	prefix    string
	runtime   *remote.RuntimeService
	plugins   []*mockPlugin
	namespace string
	sbCfg     map[string]*runtime.PodSandboxConfig
	pods      []string
	ctrs      []string
}

// setup prepares the test environment, connects all NRI plugins.
func (tc *nriTest) setup() {
	if tc.name == "" {
		tc.name = tc.t.Name()
	}
	if tc.prefix == "" {
		tc.prefix = strings.ToLower(tc.name)
	}
	if tc.namespace == "" {
		tc.namespace = tc.prefix + "-" + strconv.Itoa(os.Getpid())
	}

	tc.sbCfg = make(map[string]*runtime.PodSandboxConfig)
	tc.runtime = runtimeService

	for idx, p := range tc.plugins {
		p.namespace = tc.namespace

		if p.logf == nil {
			p.logf = tc.t.Logf
		}
		if p.idx == "" {
			p.idx = fmt.Sprintf("%02d", idx)
		}

		err := p.Start()
		require.NoError(tc.t, err, "start plugin")

		err = p.Wait(PluginSynchronized, time.After(pluginSyncTimeout))
		require.NoError(tc.t, err, "wait for plugin setup")
	}
}

// Connect a new NRI plugin to the runtime.
func (tc *nriTest) connectNewPlugin(p *mockPlugin) {
	p.namespace = tc.namespace

	if p.logf == nil {
		p.logf = tc.t.Logf
	}
	if p.idx == "" {
		p.idx = fmt.Sprintf("%02d", len(tc.plugins))
	}

	tc.plugins = append(tc.plugins, p)

	err := p.Start()
	require.NoError(tc.t, err, "start plugin")

	err = p.Wait(PluginSynchronized, time.After(pluginSyncTimeout))
	require.NoError(tc.t, err, "wait for plugin setup")
}

// Create a pod in a namespace specific to the test case.
func (tc *nriTest) runPod(name string) string {
	id, cfg := PodSandboxConfigWithCleanup(tc.t, name, tc.namespace)
	tc.sbCfg[id] = cfg
	tc.pods = append(tc.pods, id)
	return id
}

// Start a container in a (testcase-specific) pod.
func (tc *nriTest) startContainer(podID, name string, opts ...ContainerOpts) string {
	podCfg := tc.sbCfg[podID]
	require.NotNil(tc.t, podCfg, "pod config for "+podID)

	ctrCfg := ContainerConfig(name, "", opts...)
	if ctrCfg.Image.Image == "" {
		image := images.Get(images.BusyBox)
		EnsureImageExists(tc.t, image)
		ctrCfg.Image.Image = image
	}

	if len(ctrCfg.Command) == 0 {
		WithCommand("/bin/sh", "-c", "sleep 3600")(ctrCfg)
	}

	ctrID, err := tc.runtime.CreateContainer(podID, ctrCfg, podCfg)
	assert.NoError(tc.t, err, "create container")
	assert.NoError(tc.t, tc.runtime.StartContainer(ctrID), "start container")

	tc.ctrs = append(tc.ctrs, ctrID)

	tc.t.Cleanup(func() {
		assert.NoError(tc.t, tc.runtime.StopContainer(ctrID, containerStopTimeout))
		assert.NoError(tc.t, tc.runtime.RemoveContainer(ctrID))
	})

	tc.t.Logf("created/started container %s/%s/%s with ID %s",
		podCfg.Metadata.Namespace, podCfg.Metadata.Name, name, ctrID)

	return ctrID
}

//
// NRI plugin implementation for integration tests
//

type mockPlugin struct {
	name string
	idx  string
	stub stub.Stub
	mask stub.EventMask
	opts []stub.Option

	q    *eventQ
	pods map[string]*api.PodSandbox
	ctrs map[string]*api.Container

	closed               bool
	closedCh             chan struct{}
	closedOnce           sync.Once
	namespace            string
	logf                 func(string, ...any)
	synchronize          func(*mockPlugin, []*api.PodSandbox, []*api.Container) ([]*api.ContainerUpdate, error)
	runPodSandbox        func(*mockPlugin, *api.PodSandbox) error
	updatePodSandbox     func(*mockPlugin, *api.PodSandbox, *api.LinuxResources, *api.LinuxResources) error
	postUpdatePodSandbox func(*mockPlugin, *api.PodSandbox) error
	stopPodSandbox       func(*mockPlugin, *api.PodSandbox) error
	removePodSandbox     func(*mockPlugin, *api.PodSandbox) error
	createContainer      func(*mockPlugin, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error)
	postCreateContainer  func(*mockPlugin, *api.PodSandbox, *api.Container)
	updateContainer      func(*mockPlugin, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
	postUpdateContainer  func(*mockPlugin, *api.PodSandbox, *api.Container)
	stopContainer        func(*mockPlugin, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error)
}

func (m *mockPlugin) Start() error {
	var err error

	if m.name == "" {
		m.name = "mock-plugin"
	}
	if m.idx == "" {
		m.idx = "00"
	}

	if m.mask == 0 {
		m.mask.Set(
			api.Event_RUN_POD_SANDBOX,
			api.Event_UPDATE_POD_SANDBOX,
			api.Event_POST_UPDATE_POD_SANDBOX,
			api.Event_STOP_POD_SANDBOX,
			api.Event_REMOVE_POD_SANDBOX,
			api.Event_CREATE_CONTAINER,
			api.Event_POST_CREATE_CONTAINER,
			api.Event_START_CONTAINER,
			api.Event_POST_START_CONTAINER,
			api.Event_UPDATE_CONTAINER,
			api.Event_POST_UPDATE_CONTAINER,
			api.Event_STOP_CONTAINER,
			api.Event_REMOVE_CONTAINER,
		)
	}

	opts := []stub.Option{
		stub.WithPluginName(m.name),
		stub.WithPluginIdx(m.idx),
		stub.WithSocketPath(nriTestSocket),
		stub.WithOnClose(m.onClose),
	}
	opts = append(opts, m.opts...)
	m.stub, err = stub.New(m, opts...)
	if err != nil {
		return err
	}

	if m.logf == nil {
		m.logf = func(format string, args ...any) {
			fmt.Printf(format+"\n", args...)
		}
	}

	if m.synchronize == nil {
		m.synchronize = nopSynchronize
	}
	if m.runPodSandbox == nil {
		m.runPodSandbox = nopRunPodSandbox
	}
	if m.updatePodSandbox == nil {
		m.updatePodSandbox = nopUpdatePodSandbox
	}
	if m.postUpdatePodSandbox == nil {
		m.postUpdatePodSandbox = nopPostUpdatePodSandbox
	}
	if m.stopPodSandbox == nil {
		m.stopPodSandbox = nopStopPodSandbox
	}
	if m.removePodSandbox == nil {
		m.removePodSandbox = nopRemovePodSandbox
	}
	if m.createContainer == nil {
		m.createContainer = nopCreateContainer
	}
	if m.postCreateContainer == nil {
		m.postCreateContainer = nopEvent
	}
	if m.updateContainer == nil {
		m.updateContainer = nopUpdateContainer
	}
	if m.postUpdateContainer == nil {
		m.postUpdateContainer = nopEvent
	}
	if m.stopContainer == nil {
		m.stopContainer = nopStopContainer
	}

	m.q = &eventQ{}
	m.pods = make(map[string]*api.PodSandbox)
	m.ctrs = make(map[string]*api.Container)
	m.closedCh = make(chan struct{})

	err = m.stub.Start(context.Background())
	if err != nil {
		return err
	}

	return nil
}

func (m *mockPlugin) Stop() {
	if m.stub != nil {
		m.stub.Stop()
		m.stub.Wait()
	}
}

func (m *mockPlugin) onClose() {
	m.closed = true
	m.closedOnce.Do(func() { close(m.closedCh) })
	if m.stub != nil {
		m.stub.Stop()
		m.stub.Wait()
	}
}

func (m *mockPlugin) inNamespace(namespace string) bool {
	return strings.HasPrefix(namespace, m.namespace)
}

func (m *mockPlugin) Log(format string, args ...any) {
	m.logf(fmt.Sprintf("[plugin:%s-%s] ", m.idx, m.name)+format, args...)
}

func (m *mockPlugin) Configure(ctx context.Context, cfg string) (stub.EventMask, error) {
	return m.mask, nil
}

func (m *mockPlugin) Synchronize(ctx context.Context, pods []*api.PodSandbox, ctrs []*api.Container) ([]*api.ContainerUpdate, error) {
	m.Log("Synchronize")
	for _, pod := range pods {
		m.Log("  - pod %s", pod.Id)
		m.pods[pod.Id] = pod
	}
	for _, ctr := range ctrs {
		m.Log("  - ctr %s", ctr.Id)
		m.ctrs[ctr.Id] = ctr
	}

	m.q.Add(PluginSynchronized)

	return m.synchronize(m, pods, ctrs)
}

func (m *mockPlugin) Shutdown(ctx context.Context) {
	m.Log("Shutdown")
}

func (m *mockPlugin) RunPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("RunPodSandbox %s/%s", pod.Namespace, pod.Name)
	m.pods[pod.Id] = pod
	m.q.Add(PodSandboxEvent(pod, RunPodSandbox))
	return m.runPodSandbox(m, pod)
}

func (m *mockPlugin) UpdatePodSandbox(ctx context.Context, pod *api.PodSandbox, overhead *api.LinuxResources, res *api.LinuxResources) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("UpdatePodSandbox %s/%s", pod.Namespace, pod.Name)
	m.pods[pod.Id] = pod
	m.q.Add(PodSandboxEvent(pod, UpdatePodSandbox))
	return m.updatePodSandbox(m, pod, overhead, res)
}

func (m *mockPlugin) PostUpdatePodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("PostUpdatePodSandbox %s/%s", pod.Namespace, pod.Name)
	m.pods[pod.Id] = pod
	m.q.Add(PodSandboxEvent(pod, PostUpdatePodSandbox))
	return m.postUpdatePodSandbox(m, pod)
}

func (m *mockPlugin) StopPodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("StopPodSandbox %s/%s", pod.Namespace, pod.Name)
	m.pods[pod.Id] = pod
	m.q.Add(PodSandboxEvent(pod, StopPodSandbox))
	return m.stopPodSandbox(m, pod)
}

func (m *mockPlugin) RemovePodSandbox(ctx context.Context, pod *api.PodSandbox) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("RemovePodSandbox %s/%s", pod.Namespace, pod.Name)
	delete(m.pods, pod.Id)
	m.q.Add(PodSandboxEvent(pod, RemovePodSandbox))
	return m.removePodSandbox(m, pod)
}

func (m *mockPlugin) CreateContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	if !m.inNamespace(pod.Namespace) {
		return nil, nil, nil
	}

	m.Log("CreateContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, CreateContainer))

	return m.createContainer(m, pod, ctr)
}

func (m *mockPlugin) PostCreateContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("PostCreateContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, PostCreateContainer))
	m.postCreateContainer(m, pod, ctr)
	return nil
}

func (m *mockPlugin) StartContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("StartContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, StartContainer))
	return nil
}

func (m *mockPlugin) PostStartContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("PostStartContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, PostStartContainer))
	return nil
}

func (m *mockPlugin) UpdateContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) ([]*api.ContainerUpdate, error) {
	if !m.inNamespace(pod.Namespace) {
		return nil, nil
	}

	m.Log("UpdateContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, UpdateContainer))
	return m.updateContainer(m, pod, ctr)
}

func (m *mockPlugin) PostUpdateContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("PostUpdateContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, PostUpdateContainer))
	m.postUpdateContainer(m, pod, ctr)
	return nil
}

func (m *mockPlugin) StopContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) ([]*api.ContainerUpdate, error) {
	if !m.inNamespace(pod.Namespace) {
		return nil, nil
	}

	m.Log("StopContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	m.pods[pod.Id] = pod
	m.ctrs[ctr.Id] = ctr
	m.q.Add(ContainerEvent(ctr, StopContainer))
	return m.stopContainer(m, pod, ctr)
}

func (m *mockPlugin) RemoveContainer(ctx context.Context, pod *api.PodSandbox, ctr *api.Container) error {
	if !m.inNamespace(pod.Namespace) {
		return nil
	}

	m.Log("RemoveContainer %s/%s/%s", pod.Namespace, pod.Name, ctr.Name)
	delete(m.pods, pod.Id)
	delete(m.ctrs, ctr.Id)
	m.q.Add(ContainerEvent(ctr, RemoveContainer))
	return nil
}

func (m *mockPlugin) Wait(e *Event, deadline <-chan time.Time) error {
	_, err := m.q.Wait(e, deadline)
	return err
}

func nopSynchronize(*mockPlugin, []*api.PodSandbox, []*api.Container) ([]*api.ContainerUpdate, error) {
	return nil, nil
}

func nopRunPodSandbox(*mockPlugin, *api.PodSandbox) error {
	return nil
}

func nopUpdatePodSandbox(*mockPlugin, *api.PodSandbox, *api.LinuxResources, *api.LinuxResources) error {
	return nil
}

func nopPostUpdatePodSandbox(*mockPlugin, *api.PodSandbox) error {
	return nil
}

func nopStopPodSandbox(*mockPlugin, *api.PodSandbox) error {
	return nil
}

func nopRemovePodSandbox(*mockPlugin, *api.PodSandbox) error {
	return nil
}

func nopCreateContainer(*mockPlugin, *api.PodSandbox, *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	return nil, nil, nil
}

func nopUpdateContainer(*mockPlugin, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
	return nil, nil
}

func nopStopContainer(*mockPlugin, *api.PodSandbox, *api.Container) ([]*api.ContainerUpdate, error) {
	return nil, nil
}

func nopEvent(*mockPlugin, *api.PodSandbox, *api.Container) {
}

//
// plugin event and event recording
//

type EventType string

const (
	Configured   = "configured"
	Synchronized = "synchronized"
	StartupError = "startup-error"
	Shutdown     = "shutdown"
	Disconnected = "closed"
	Stopped      = "stopped"

	RunPodSandbox        = "RunPodSandbox"
	UpdatePodSandbox     = "UpdatePodSandbox"
	PostUpdatePodSandbox = "PostUpdatePodSandbox"
	StopPodSandbox       = "StopPodSandbox"
	RemovePodSandbox     = "RemovePodSandbox"
	CreateContainer      = "CreateContainer"
	StartContainer       = "StartContainer"
	UpdateContainer      = "UpdateContainer"
	StopContainer        = "StopContainer"
	RemoveContainer      = "RemoveContainer"
	PostCreateContainer  = "PostCreateContainer"
	PostStartContainer   = "PostStartContainer"
	PostUpdateContainer  = "PostUpdateContainer"

	Error   = "Error"
	Timeout = ""
)

type Event struct {
	Type EventType
	Pod  string
	Ctr  string
}

var (
	PluginConfigured   = &Event{Type: Configured}
	PluginSynchronized = &Event{Type: Synchronized}
	PluginStartupError = &Event{Type: StartupError}
	PluginShutdown     = &Event{Type: Shutdown}
	PluginDisconnected = &Event{Type: Disconnected}
	PluginStopped      = &Event{Type: Stopped}
)

func PodSandboxEvent(pod *api.PodSandbox, t EventType) *Event {
	return &Event{Type: t, Pod: pod.Id}
}

func ContainerEvent(ctr *api.Container, t EventType) *Event {
	return &Event{Type: t, Ctr: ctr.Id}
}

func PodRefEvent(pod string, t EventType) *Event {
	return &Event{Type: t, Pod: pod}
}

func ContainerRefEvent(pod, ctr string, t EventType) *Event {
	return &Event{Type: t, Pod: pod, Ctr: ctr}
}

func (e *Event) Matches(o *Event) bool {
	if e.Type != o.Type {
		return false
	}
	if e.Pod != "" && o.Pod != "" {
		if e.Pod != o.Pod {
			return false
		}
	}
	if e.Ctr != "" && o.Ctr != "" {
		if e.Ctr != o.Ctr {
			return false
		}
	}
	return true
}

func (e *Event) String() string {
	str, sep := "", ""
	if e.Pod != "" {
		str = e.Pod
		sep = ":"
	}
	if e.Ctr != "" {
		str += sep + e.Ctr
		sep = "/"
	}
	return str + sep + string(e.Type)
}

type eventQ struct {
	sync.Mutex
	q []*Event
	c chan *Event
}

func (q *eventQ) Add(e *Event) {
	if q == nil {
		return
	}
	q.Lock()
	defer q.Unlock()
	q.q = append(q.q, e)
	if q.c != nil {
		q.c <- e
	}
}

func (q *eventQ) Reset(e *Event) {
	q.Lock()
	defer q.Unlock()
	q.q = []*Event{}
}

func (q *eventQ) Events() []*Event {
	q.Lock()
	defer q.Unlock()
	var events []*Event
	events = append(events, q.q...)
	return events
}

func (q *eventQ) Has(e *Event) bool {
	q.Lock()
	defer q.Unlock()
	return q.search(e) != nil
}

func (q *eventQ) search(e *Event) *Event {
	for _, qe := range q.q {
		if qe.Matches(e) {
			return qe
		}
	}
	return nil
}

func (q *eventQ) Wait(w *Event, deadline <-chan time.Time) (*Event, error) {
	var unlocked bool
	q.Lock()
	defer func() {
		if !unlocked {
			q.Unlock()
		}
	}()

	if e := q.search(w); e != nil {
		return e, nil
	}

	if q.c != nil {
		return nil, fmt.Errorf("event queue already busy Wait()ing")
	}
	q.c = make(chan *Event, 16)
	defer func() {
		c := q.c
		q.c = nil
		close(c)
	}()

	q.Unlock()
	unlocked = true
	for {
		select {
		case e := <-q.c:
			if e.Matches(w) {
				return e, nil
			}
		case <-deadline:
			return nil, fmt.Errorf("event queue timed out Wait()ing for %s", w)
		}
	}
}

//
// helper functions
//

// Wait for a file to show up in the filesystem then read its content.
func waitForFileAndRead(path string, timeout time.Duration) ([]byte, error) {
	var (
		deadline = time.After(timeout)
		slack    = 100 * time.Millisecond
	)

WAIT:
	for {
		if _, err := os.Stat(path); err == nil {
			break WAIT
		}
		select {
		case <-deadline:
			return nil, fmt.Errorf("waiting for %s timed out", path)
		default:
			time.Sleep(slack)
		}
	}

	time.Sleep(slack)
	return os.ReadFile(path)
}

// getAvailableCpuset returns the set of online CPUs.
func getAvailableCpuset(t *testing.T) []string {
	if availableCpuset == nil {
		availableCpuset = getXxxset(t, "cpuset", onlineCpus)
	}
	return availableCpuset
}

// getAvailableMemset returns the set of usable NUMA nodes.
func getAvailableMemset(t *testing.T) []string {
	if availableMemset == nil {
		availableMemset = getXxxset(t, "memset", normalMems)
	}
	return availableMemset
}

// getXxxset reads/parses a CPU/memory set into a slice.
func getXxxset(t *testing.T, kind, path string) []string {
	var (
		data []byte
		set  []string
		one  uint64
		err  error
	)

	data, err = os.ReadFile(path)
	if err != nil {
		t.Logf("failed to read %s: %v", path, err)
		return nil
	}

	for rng := range strings.SplitSeq(strings.TrimSpace(string(data)), ",") {
		var (
			lo int
			hi = -1
		)
		loHi := strings.Split(rng, "-")
		switch len(loHi) {
		case 2:
			one, err = strconv.ParseUint(loHi[1], 10, 32)
			if err != nil {
				t.Errorf("failed to parse %s range %q: %v", kind, rng, err)
				return nil
			}
			hi = int(one) + 1
			fallthrough
		case 1:
			one, err = strconv.ParseUint(loHi[0], 10, 32)
			if err != nil {
				t.Errorf("failed to parse %s range %q: %v", kind, rng, err)
				return nil
			}
			lo = int(one)
		default:
			t.Errorf("invalid %s range %q", kind, rng)
			return nil
		}

		if hi == -1 {
			hi = lo + 1
		}

		for i := lo; i < hi; i++ {
			set = append(set, strconv.Itoa(i))
		}
	}

	return set
}
