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
	"fmt"
	"sync"

	"github.com/containerd/log"

	"github.com/containerd/containerd/v2/version"
	nri "github.com/containerd/nri/pkg/adaptation"
)

// API implements a common API for interfacing NRI from containerd. It is
// agnostic to any internal containerd implementation details of pods and
// containers. It needs corresponding Domain interfaces for each containerd
// namespace it needs to handle. These domains take care of the namespace-
// specific details of providing pod and container metadata to NRI and of
// applying NRI-requested adjustments to the state of containers.
type API interface {
	// IsEnabled returns true if the NRI interface is enabled and initialized.
	IsEnabled() bool

	// Start starts the NRI interface, allowing external NRI plugins to
	// connect, register, and hook themselves into the lifecycle events
	// of pods and containers.
	Start() error

	// Stop stops the NRI interface.
	Stop()

	// RunPodSandbox relays pod creation events to NRI.
	RunPodSandbox(context.Context, PodSandbox) error

	// StopPodSandbox relays pod shutdown events to NRI.
	StopPodSandbox(context.Context, PodSandbox) error

	// RemovePodSandbox relays pod removal events to NRI.
	RemovePodSandbox(context.Context, PodSandbox) error

	// CreateContainer relays container creation requests to NRI.
	CreateContainer(context.Context, PodSandbox, Container) (*nri.ContainerAdjustment, error)

	// PostCreateContainer relays successful container creation events to NRI.
	PostCreateContainer(context.Context, PodSandbox, Container) error

	// StartContainer relays container start request notifications to NRI.
	StartContainer(context.Context, PodSandbox, Container) error

	// PostStartContainer relays successful container startup events to NRI.
	PostStartContainer(context.Context, PodSandbox, Container) error

	// UpdateContainer relays container update requests to NRI.
	UpdateContainer(context.Context, PodSandbox, Container, *nri.LinuxResources) (*nri.LinuxResources, error)

	// PostUpdateContainer relays successful container update events to NRI.
	PostUpdateContainer(context.Context, PodSandbox, Container) error

	// StopContainer relays container stop requests to NRI.
	StopContainer(context.Context, PodSandbox, Container) error

	// NotifyContainerExit handles the exit event of a container.
	NotifyContainerExit(context.Context, PodSandbox, Container)

	// RemoveContainer relays container removal events to NRI.
	RemoveContainer(context.Context, PodSandbox, Container) error

	// BlockPluginSync blocks plugin synchronization until it is Unblock()ed.
	BlockPluginSync() *PluginSyncBlock
}

type State int

const (
	Created State = iota + 1
	Running
	Stopped
	Removed
)

type local struct {
	sync.Mutex
	cfg *Config
	nri *nri.Adaptation

	state map[string]State
}

var _ API = &local{}

// New creates an instance of the NRI interface with the given configuration.
func New(cfg *Config) (API, error) {
	l := &local{
		cfg: cfg,
	}

	if cfg.Disable {
		log.L.Info("NRI interface is disabled by configuration.")
		return l, nil
	}

	var (
		name     = version.Name
		version  = version.Version
		opts     = cfg.toOptions()
		syncFn   = l.syncPlugin
		updateFn = l.updateFromPlugin
		err      error
	)

	cfg.ConfigureTimeouts()

	l.nri, err = nri.New(name, version, syncFn, updateFn, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize NRI interface: %w", err)
	}

	l.state = make(map[string]State)

	log.L.Info("created NRI interface")

	return l, nil
}

func (l *local) IsEnabled() bool {
	return l != nil && !l.cfg.Disable
}

func (l *local) Start() error {
	if !l.IsEnabled() {
		return nil
	}

	err := l.nri.Start()
	if err != nil {
		return fmt.Errorf("failed to start NRI interface: %w", err)
	}

	return nil
}

func (l *local) Stop() {
	if !l.IsEnabled() {
		return
	}

	l.Lock()
	defer l.Unlock()

	l.nri.Stop()
	l.nri = nil
}

func (l *local) RunPodSandbox(ctx context.Context, pod PodSandbox) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.RunPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := l.nri.RunPodSandbox(ctx, request)
	l.setState(pod.GetID(), Running)
	return err
}

func (l *local) StopPodSandbox(ctx context.Context, pod PodSandbox) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	if !l.needsStopping(pod.GetID()) {
		return nil
	}

	request := &nri.StopPodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := l.nri.StopPodSandbox(ctx, request)
	l.setState(pod.GetID(), Stopped)
	return err
}

func (l *local) RemovePodSandbox(ctx context.Context, pod PodSandbox) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	if !l.needsRemoval(pod.GetID()) {
		return nil
	}

	request := &nri.RemovePodSandboxRequest{
		Pod: podSandboxToNRI(pod),
	}

	err := l.nri.RemovePodSandbox(ctx, request)
	l.setState(pod.GetID(), Removed)
	return err
}

func (l *local) CreateContainer(ctx context.Context, pod PodSandbox, ctr Container) (*nri.ContainerAdjustment, error) {
	if !l.IsEnabled() {
		return nil, nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.CreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	response, err := l.nri.CreateContainer(ctx, request)
	l.setState(request.Container.Id, Created)
	if err != nil {
		return nil, err
	}

	_, err = l.evictContainers(ctx, response.Evict)
	if err != nil {
		// TODO(klihub): we ignore pre-create eviction failures for now
		log.G(ctx).WithError(err).Warnf("pre-create eviction failed")
	}

	if _, err := l.applyUpdates(ctx, response.Update); err != nil {
		// TODO(klihub): we ignore pre-create update failures for now
		log.G(ctx).WithError(err).Warnf("pre-create update failed")
	}

	return response.Adjust, nil
}

func (l *local) PostCreateContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.PostCreateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	return l.nri.PostCreateContainer(ctx, request)
}

func (l *local) StartContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.StartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	err := l.nri.StartContainer(ctx, request)
	l.setState(request.Container.Id, Running)

	return err
}

func (l *local) PostStartContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.PostStartContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	return l.nri.PostStartContainer(ctx, request)
}

func (l *local) UpdateContainer(ctx context.Context, pod PodSandbox, ctr Container, req *nri.LinuxResources) (*nri.LinuxResources, error) {
	if !l.IsEnabled() {
		return nil, nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.UpdateContainerRequest{
		Pod:            podSandboxToNRI(pod),
		Container:      containerToNRI(ctr),
		LinuxResources: req,
	}

	response, err := l.nri.UpdateContainer(ctx, request)
	if err != nil {
		return nil, err
	}

	_, err = l.evictContainers(ctx, response.Evict)
	if err != nil {
		// TODO(klihub): we ignore pre-update eviction failures for now
		log.G(ctx).WithError(err).Warnf("pre-update eviction failed")
	}

	cnt := len(response.Update)
	if cnt == 0 {
		return nil, nil
	}

	if cnt > 1 {
		_, err = l.applyUpdates(ctx, response.Update[0:cnt-1])
		if err != nil {
			// TODO(klihub): we ignore pre-update update failures for now
			log.G(ctx).WithError(err).Warnf("pre-update update failed")
		}
	}

	return response.Update[cnt-1].GetLinux().GetResources(), nil
}

func (l *local) PostUpdateContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	request := &nri.PostUpdateContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	return l.nri.PostUpdateContainer(ctx, request)
}

func (l *local) StopContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	return l.stopContainer(ctx, pod, ctr)
}

func (l *local) NotifyContainerExit(ctx context.Context, pod PodSandbox, ctr Container) {
	go func() {
		l.Lock()
		defer l.Unlock()
		l.stopContainer(ctx, pod, ctr)
	}()
}

func (l *local) stopContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.needsStopping(ctr.GetID()) {
		log.G(ctx).Tracef("NRI stopContainer: container %s does not need stopping",
			ctr.GetID())
		return nil
	}

	request := &nri.StopContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}

	response, err := l.nri.StopContainer(ctx, request)
	l.setState(request.Container.Id, Stopped)
	if err != nil {
		return err
	}

	_, err = l.applyUpdates(ctx, response.Update)
	if err != nil {
		// TODO(klihub): we ignore post-stop update failures for now
		log.G(ctx).WithError(err).Warnf("post-stop update failed")
	}

	return nil
}

func (l *local) RemoveContainer(ctx context.Context, pod PodSandbox, ctr Container) error {
	if !l.IsEnabled() {
		return nil
	}

	l.Lock()
	defer l.Unlock()

	if !l.needsRemoval(ctr.GetID()) {
		return nil
	}

	l.stopContainer(ctx, pod, ctr)

	request := &nri.RemoveContainerRequest{
		Pod:       podSandboxToNRI(pod),
		Container: containerToNRI(ctr),
	}
	err := l.nri.RemoveContainer(ctx, request)
	l.setState(request.Container.Id, Removed)

	return err
}

type PluginSyncBlock = nri.PluginSyncBlock

func (l *local) BlockPluginSync() *PluginSyncBlock {
	if !l.IsEnabled() {
		return nil
	}
	return l.nri.BlockPluginSync()
}

func (l *local) syncPlugin(ctx context.Context, syncFn nri.SyncCB) error {
	l.Lock()
	defer l.Unlock()

	log.G(ctx).Info("Synchronizing NRI (plugin) with current runtime state")

	pods := podSandboxesToNRI(domains.listPodSandboxes())
	containers := containersToNRI(domains.listContainers())

	for _, ctr := range containers {
		switch ctr.GetState() {
		case nri.ContainerState_CONTAINER_CREATED:
			l.setState(ctr.GetId(), Created)
		case nri.ContainerState_CONTAINER_RUNNING, nri.ContainerState_CONTAINER_PAUSED:
			l.setState(ctr.GetId(), Running)
		case nri.ContainerState_CONTAINER_STOPPED:
			l.setState(ctr.GetId(), Stopped)
		default:
			l.setState(ctr.GetId(), Removed)
		}
	}

	updates, err := syncFn(ctx, pods, containers)
	if err != nil {
		return err
	}

	_, err = l.applyUpdates(ctx, updates)
	if err != nil {
		// TODO(klihub): we ignore post-sync update failures for now
		log.G(ctx).WithError(err).Warnf("post-sync update failed")
	}

	return nil
}

func (l *local) updateFromPlugin(ctx context.Context, req []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	l.Lock()
	defer l.Unlock()

	log.G(ctx).Trace("Unsolicited NRI container updates")

	failed, err := l.applyUpdates(ctx, req)
	return failed, err
}

func (l *local) applyUpdates(ctx context.Context, updates []*nri.ContainerUpdate) ([]*nri.ContainerUpdate, error) {
	// TODO(klihub): should we pre-save state and attempt a rollback on failure ?
	failed, err := domains.updateContainers(ctx, updates)
	return failed, err
}

func (l *local) evictContainers(ctx context.Context, evict []*nri.ContainerEviction) ([]*nri.ContainerEviction, error) {
	failed, err := domains.evictContainers(ctx, evict)
	return failed, err
}

func (l *local) setState(id string, state State) {
	if state != Removed {
		l.state[id] = state
		return
	}

	delete(l.state, id)
}

func (l *local) getState(id string) State {
	if state, ok := l.state[id]; ok {
		return state
	}

	return Removed
}

func (l *local) needsStopping(id string) bool {
	s := l.getState(id)
	if s == Created || s == Running {
		return true
	}
	return false
}

func (l *local) needsRemoval(id string) bool {
	s := l.getState(id)
	if s == Created || s == Running || s == Stopped {
		return true
	}
	return false
}
