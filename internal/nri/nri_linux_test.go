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

package nri

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	nri "github.com/containerd/nri/pkg/adaptation"
	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
)

type fakePodSandbox struct {
	id   string
	name string
	uid  string
	ns   string
}

func (p *fakePodSandbox) GetDomain() string                   { return "k8s.io" }
func (p *fakePodSandbox) GetID() string                       { return p.id }
func (p *fakePodSandbox) GetName() string                     { return p.name }
func (p *fakePodSandbox) GetUID() string                      { return p.uid }
func (p *fakePodSandbox) GetNamespace() string                { return p.ns }
func (p *fakePodSandbox) GetLabels() map[string]string        { return nil }
func (p *fakePodSandbox) GetAnnotations() map[string]string   { return nil }
func (p *fakePodSandbox) GetRuntimeHandler() string           { return "" }
func (p *fakePodSandbox) GetLinuxPodSandbox() LinuxPodSandbox { return p }
func (p *fakePodSandbox) GetPid() uint32                      { return 0 }
func (p *fakePodSandbox) GetIPs() []string                    { return nil }

func (p *fakePodSandbox) GetLinuxNamespaces() []*nri.LinuxNamespace { return nil }
func (p *fakePodSandbox) GetPodLinuxOverhead() *nri.LinuxResources  { return nil }
func (p *fakePodSandbox) GetPodLinuxResources() *nri.LinuxResources { return nil }
func (p *fakePodSandbox) GetCgroupParent() string                   { return "" }
func (p *fakePodSandbox) GetCgroupsPath() string                    { return "" }
func (p *fakePodSandbox) GetLinuxResources() *nri.LinuxResources    { return nil }

type fakeDomain struct {
	mu   sync.Mutex
	name string
	pods []PodSandbox
}

func (d *fakeDomain) GetName() string { return d.name }

func (d *fakeDomain) setPods(pods []PodSandbox) {
	d.mu.Lock()
	d.pods = pods
	d.mu.Unlock()
}

func (d *fakeDomain) ListPodSandboxes() []PodSandbox {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.pods)
}

func (d *fakeDomain) ListContainers() []Container { return nil }

func (d *fakeDomain) GetPodSandbox(id string) (PodSandbox, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, pod := range d.pods {
		if pod.GetID() == id {
			return pod, true
		}
	}
	return nil, false
}

func (d *fakeDomain) GetContainer(string) (Container, bool) { return nil, false }

func (d *fakeDomain) UpdateContainer(context.Context, *nri.ContainerUpdate) error { return nil }

func (d *fakeDomain) EvictContainer(context.Context, *nri.ContainerEviction) error { return nil }

type recordingPlugin struct {
	mu        sync.Mutex
	syncedCh  chan struct{}
	syncOnce  sync.Once
	syncedIDs []string
	stopped   []string
	removed   []string
}

func newRecordingPlugin() *recordingPlugin {
	return &recordingPlugin{
		syncedCh: make(chan struct{}),
	}
}

func (p *recordingPlugin) Synchronize(_ context.Context, pods []*api.PodSandbox, _ []*api.Container) ([]*api.ContainerUpdate, error) {
	p.mu.Lock()
	p.syncedIDs = make([]string, 0, len(pods))
	for _, pod := range pods {
		p.syncedIDs = append(p.syncedIDs, pod.GetId())
	}
	p.mu.Unlock()
	p.syncOnce.Do(func() {
		close(p.syncedCh)
	})
	return nil, nil
}

func (p *recordingPlugin) StopPodSandbox(_ context.Context, pod *api.PodSandbox) error {
	p.mu.Lock()
	p.stopped = append(p.stopped, pod.GetId())
	p.mu.Unlock()
	return nil
}

func (p *recordingPlugin) RemovePodSandbox(_ context.Context, pod *api.PodSandbox) error {
	p.mu.Lock()
	p.removed = append(p.removed, pod.GetId())
	p.mu.Unlock()
	return nil
}

func (p *recordingPlugin) synced() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.syncedIDs)
}

func (p *recordingPlugin) events() (stopped, removed []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.stopped), slices.Clone(p.removed)
}

func withFakeDomain(t *testing.T, d *fakeDomain) {
	t.Helper()
	domains.Lock()
	prev := domains.domains
	domains.domains = map[string]Domain{d.GetName(): d}
	domains.Unlock()

	t.Cleanup(func() {
		domains.Lock()
		domains.domains = prev
		domains.Unlock()
	})
}

func startTestLocalNRI(t *testing.T, d *fakeDomain) (API, string) {
	t.Helper()
	withFakeDomain(t, d)

	sockDir := t.TempDir()
	emptyDir := filepath.Join(sockDir, "empty")
	if err := os.MkdirAll(emptyDir, 0o750); err != nil {
		t.Fatalf("create empty plugin dir: %v", err)
	}
	sockPath := filepath.Join(sockDir, "nri.sock")

	cfg := DefaultConfig()
	cfg.SocketPath = sockPath
	cfg.PluginPath = emptyDir
	cfg.PluginConfigPath = emptyDir
	cfg.DefaultValidator = nil

	nriAPI, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := nriAPI.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(nriAPI.Stop)
	return nriAPI, sockPath
}

func connectRecordingPlugin(t *testing.T, nriAPI API, sockPath, idx, name string) *recordingPlugin {
	t.Helper()
	rec := newRecordingPlugin()
	s, err := stub.New(rec,
		stub.WithPluginIdx(idx),
		stub.WithPluginName(name),
		stub.WithSocketPath(sockPath),
	)
	if err != nil {
		t.Fatalf("stub.New(%s): %v", name, err)
	}
	if err := s.Start(t.Context()); err != nil {
		t.Fatalf("stub.Start(%s): %v", name, err)
	}
	t.Cleanup(s.Stop)

	select {
	case <-rec.syncedCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for plugin %s to synchronize", name)
	}

	// Synchronize closes syncedCh before adaptation appends the plugin to its
	// active plugin list and releases syncLock. Acquiring and releasing
	// BlockPluginSync waits for registration to finish so subsequent lifecycle
	// calls are guaranteed to see the plugin.
	nriAPI.BlockPluginSync().Unblock()
	return rec
}

func TestSyncPluginSeedsPodSandboxStateAfterRestart(t *testing.T) {
	ctx := t.Context()

	pod1 := &fakePodSandbox{id: "pod-1", name: "p1", uid: "uid-1", ns: "default"}
	pod2 := &fakePodSandbox{id: "pod-2", name: "p2", uid: "uid-2", ns: "default"}
	podUnknown := &fakePodSandbox{id: "pod-unknown", name: "unknown", uid: "uid-3", ns: "default"}

	dom := &fakeDomain{
		name: "k8s.io",
		pods: []PodSandbox{pod1, pod2},
	}

	// Starting a fresh local NRI instance with pre-existing pods in the domain
	// mirrors containerd recovering sandboxes into sandboxStore after a restart
	// while local.state starts empty.
	nriAPI, sockPath := startTestLocalNRI(t, dom)
	plugin := connectRecordingPlugin(t, nriAPI, sockPath, "01", "test-plugin")

	if synced := plugin.synced(); !slices.Equal(synced, []string{pod1.id, pod2.id}) {
		t.Errorf("Synchronize pods = %v, want %v", synced, []string{pod1.id, pod2.id})
	}

	// Recovered pods receive StopPodSandbox once and RemovePodSandbox once,
	// with duplicate StopPodSandbox / RemovePodSandbox calls suppressed.
	if err := nriAPI.StopPodSandbox(ctx, pod1); err != nil {
		t.Fatalf("StopPodSandbox(pod1): %v", err)
	}
	if err := nriAPI.StopPodSandbox(ctx, pod1); err != nil {
		t.Fatalf("second StopPodSandbox(pod1): %v", err)
	}
	if err := nriAPI.RemovePodSandbox(ctx, pod1); err != nil {
		t.Fatalf("RemovePodSandbox(pod1): %v", err)
	}
	if err := nriAPI.RemovePodSandbox(ctx, pod1); err != nil {
		t.Fatalf("second RemovePodSandbox(pod1): %v", err)
	}

	if err := nriAPI.StopPodSandbox(ctx, pod2); err != nil {
		t.Fatalf("StopPodSandbox(pod2): %v", err)
	}
	if err := nriAPI.RemovePodSandbox(ctx, pod2); err != nil {
		t.Fatalf("RemovePodSandbox(pod2): %v", err)
	}

	// Untracked pod not present during sync receives neither event.
	if err := nriAPI.StopPodSandbox(ctx, podUnknown); err != nil {
		t.Fatalf("StopPodSandbox(podUnknown): %v", err)
	}
	if err := nriAPI.RemovePodSandbox(ctx, podUnknown); err != nil {
		t.Fatalf("RemovePodSandbox(podUnknown): %v", err)
	}

	stopped, removed := plugin.events()
	want := []string{pod1.id, pod2.id}
	if !slices.Equal(stopped, want) {
		t.Errorf("StopPodSandbox events = %v, want %v", stopped, want)
	}
	if !slices.Equal(removed, want) {
		t.Errorf("RemovePodSandbox events = %v, want %v", removed, want)
	}
}

func TestSyncPluginPreservesTrackedPodSandboxState(t *testing.T) {
	ctx := t.Context()

	dom := &fakeDomain{name: "k8s.io"}
	nriAPI, sockPath := startTestLocalNRI(t, dom)
	plugin1 := connectRecordingPlugin(t, nriAPI, sockPath, "01", "plugin-1")

	podStoppedAlready := &fakePodSandbox{id: "pod-stopped-already", name: "p1", uid: "u1", ns: "default"}
	podRunning := &fakePodSandbox{id: "pod-running", name: "p2", uid: "u2", ns: "default"}

	if err := nriAPI.RunPodSandbox(ctx, podStoppedAlready); err != nil {
		t.Fatalf("RunPodSandbox(podStoppedAlready): %v", err)
	}
	if err := nriAPI.StopPodSandbox(ctx, podStoppedAlready); err != nil {
		t.Fatalf("StopPodSandbox(podStoppedAlready): %v", err)
	}
	if err := nriAPI.RunPodSandbox(ctx, podRunning); err != nil {
		t.Fatalf("RunPodSandbox(podRunning): %v", err)
	}

	// ListPodSandboxes returns both Ready and NotReady sandboxes until removal.
	// Connecting a new plugin triggers syncPlugin, which must not reset
	// podStoppedAlready from Stopped back to Running.
	dom.setPods([]PodSandbox{podStoppedAlready, podRunning})
	plugin2 := connectRecordingPlugin(t, nriAPI, sockPath, "02", "plugin-2")

	if synced := plugin2.synced(); !slices.Equal(synced, []string{podStoppedAlready.id, podRunning.id}) {
		t.Errorf("plugin-2 Synchronize pods = %v, want %v", synced, []string{podStoppedAlready.id, podRunning.id})
	}

	// Idempotent StopPodSandbox on already-stopped pod must be a no-op.
	if err := nriAPI.StopPodSandbox(ctx, podStoppedAlready); err != nil {
		t.Fatalf("second StopPodSandbox(podStoppedAlready): %v", err)
	}
	if err := nriAPI.RemovePodSandbox(ctx, podStoppedAlready); err != nil {
		t.Fatalf("RemovePodSandbox(podStoppedAlready): %v", err)
	}

	// StopPodSandbox and RemovePodSandbox on podRunning reach both plugins.
	if err := nriAPI.StopPodSandbox(ctx, podRunning); err != nil {
		t.Fatalf("StopPodSandbox(podRunning): %v", err)
	}
	if err := nriAPI.RemovePodSandbox(ctx, podRunning); err != nil {
		t.Fatalf("RemovePodSandbox(podRunning): %v", err)
	}

	stopped1, removed1 := plugin1.events()
	wantStopped1 := []string{podStoppedAlready.id, podRunning.id}
	wantRemoved1 := []string{podStoppedAlready.id, podRunning.id}
	if !slices.Equal(stopped1, wantStopped1) {
		t.Errorf("plugin-1 StopPodSandbox events = %v, want %v", stopped1, wantStopped1)
	}
	if !slices.Equal(removed1, wantRemoved1) {
		t.Errorf("plugin-1 RemovePodSandbox events = %v, want %v", removed1, wantRemoved1)
	}

	stopped2, removed2 := plugin2.events()
	wantStopped2 := []string{podRunning.id}
	wantRemoved2 := []string{podStoppedAlready.id, podRunning.id}
	if !slices.Equal(stopped2, wantStopped2) {
		t.Errorf("plugin-2 StopPodSandbox events = %v, want %v", stopped2, wantStopped2)
	}
	if !slices.Equal(removed2, wantRemoved2) {
		t.Errorf("plugin-2 RemovePodSandbox events = %v, want %v", removed2, wantRemoved2)
	}
}
