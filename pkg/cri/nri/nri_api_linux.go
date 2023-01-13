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
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/blockio"
	"github.com/containerd/containerd/pkg/cri/annotations"
	"github.com/containerd/containerd/pkg/cri/constants"
	cstore "github.com/containerd/containerd/pkg/cri/store/container"
	sstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	cri "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/nri"
	"github.com/containerd/nri/pkg/api"
	nrigen "github.com/containerd/nri/pkg/runtime-tools/generate"
)

type API struct {
	cri CRIImplementation
	nri nri.API
}

func NewAPI(nri nri.API) *API {
	return &API{
		nri: nri,
	}
}

func (a *API) IsDisabled() bool {
	return a == nil || a.nri == nil || !a.nri.IsEnabled()
}

func (a *API) IsEnabled() bool { return !a.IsDisabled() }

func (a *API) Register(cri CRIImplementation) error {
	if a.IsDisabled() {
		return nil
	}

	a.cri = cri
	nri.RegisterDomain(a)

	return a.nri.Start()
}

//
// CRI-NRI lifecycle hook interface
//
// These functions are used to hook NRI into the processing of
// the corresponding CRI lifecycle events using the common NRI
// interface.
//

func (a *API) RunPodSandbox(ctx context.Context, criPod *sstore.Sandbox) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	err := a.nri.RunPodSandbox(ctx, pod)

	if err != nil {
		a.nri.StopPodSandbox(ctx, pod)
		a.nri.RemovePodSandbox(ctx, pod)
	}

	return err
}

func (a *API) StopPodSandbox(ctx context.Context, criPod *sstore.Sandbox) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	err := a.nri.StopPodSandbox(ctx, pod)

	return err
}

func (a *API) RemovePodSandbox(ctx context.Context, criPod *sstore.Sandbox) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)

	err := a.nri.RemovePodSandbox(ctx, pod)

	return err
}

func (a *API) CreateContainer(ctx context.Context, ctrs *containers.Container, spec *specs.Spec) (*api.ContainerAdjustment, error) {
	ctr := a.nriContainer(ctrs, spec)

	criPod, err := a.cri.SandboxStore().Get(ctr.GetPodSandboxID())
	if err != nil {
		return nil, err
	}

	pod := a.nriPodSandbox(&criPod)

	adjust, err := a.nri.CreateContainer(ctx, pod, ctr)

	return adjust, err
}

func (a *API) PostCreateContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(criCtr, nil)

	err := a.nri.PostCreateContainer(ctx, pod, ctr)

	return err
}

func (a *API) StartContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(criCtr, nil)

	err := a.nri.StartContainer(ctx, pod, ctr)

	return err
}

func (a *API) PostStartContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(criCtr, nil)

	err := a.nri.PostStartContainer(ctx, pod, ctr)

	return err
}

func (a *API) UpdateContainerResources(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container, req *cri.LinuxContainerResources) (*cri.LinuxContainerResources, error) {
	if a.IsDisabled() {
		return nil, nil
	}

	const noOomAdj = 0

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(criCtr, nil)

	r, err := a.nri.UpdateContainer(ctx, pod, ctr, api.FromCRILinuxResources(req))
	if err != nil {
		return nil, err
	}

	return r.ToCRI(noOomAdj), nil
}

func (a *API) PostUpdateContainerResources(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(criCtr, nil)

	err := a.nri.PostUpdateContainer(ctx, pod, ctr)

	return err
}

func (a *API) StopContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	if a.IsDisabled() {
		return nil
	}

	ctr := a.nriContainer(criCtr, nil)

	if criPod == nil || criPod.ID == "" {
		criPod = &sstore.Sandbox{
			Metadata: sstore.Metadata{
				ID: ctr.GetPodSandboxID(),
			},
		}
	}
	pod := a.nriPodSandbox(criPod)

	err := a.nri.StopContainer(ctx, pod, ctr)

	return err
}

func (a *API) NotifyContainerExit(ctx context.Context, criCtr *cstore.Container) {
	if a.IsDisabled() {
		return
	}

	ctr := a.nriContainer(criCtr, nil)

	criPod, _ := a.cri.SandboxStore().Get(ctr.GetPodSandboxID())
	if criPod.ID == "" {
		criPod = sstore.Sandbox{
			Metadata: sstore.Metadata{
				ID: ctr.GetPodSandboxID(),
			},
		}
	}
	pod := a.nriPodSandbox(&criPod)

	a.nri.NotifyContainerExit(ctx, pod, ctr)
}

func (a *API) RemoveContainer(ctx context.Context, criPod *sstore.Sandbox, criCtr *cstore.Container) error {
	if a.IsDisabled() {
		return nil
	}

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(criCtr, nil)

	err := a.nri.RemoveContainer(ctx, pod, ctr)

	return err
}

func (a *API) UndoCreateContainer(ctx context.Context, criPod *sstore.Sandbox, id string, spec *specs.Spec) {
	if a.IsDisabled() {
		return
	}

	pod := a.nriPodSandbox(criPod)
	ctr := a.nriContainer(&containers.Container{ID: id}, spec)

	err := a.nri.StopContainer(ctx, pod, ctr)
	if err != nil {
		log.G(ctx).WithError(err).Error("container creation undo (stop) failed")
	}

	err = a.nri.RemoveContainer(ctx, pod, ctr)
	if err != nil {
		log.G(ctx).WithError(err).Error("container creation undo (remove) failed")
	}
}

func (a *API) WithContainerAdjustment() containerd.NewContainerOpts {
	if a.IsDisabled() {
		return func(context.Context, *containerd.Client, *containers.Container) error {
			return nil
		}
	}

	resourceCheckOpt := nrigen.WithResourceChecker(
		func(r *runtimespec.LinuxResources) error {
			if r != nil {
				if a.cri.Config().DisableHugetlbController {
					r.HugepageLimits = nil
				}
			}
			return nil
		},
	)

	rdtResolveOpt := nrigen.WithRdtResolver(
		func(className string) (*runtimespec.LinuxIntelRdt, error) {
			if className == "" {
				return nil, nil
			}
			return &runtimespec.LinuxIntelRdt{
				ClosID: className,
			}, nil
		},
	)

	blkioResolveOpt := nrigen.WithBlockIOResolver(
		func(className string) (*runtimespec.LinuxBlockIO, error) {
			if className == "" {
				return nil, nil
			}
			blockIO, err := blockio.ClassNameToLinuxOCI(className)
			if err != nil {
				return nil, err
			}
			return blockIO, nil
		},
	)

	return func(ctx context.Context, _ *containerd.Client, c *containers.Container) error {
		spec := &specs.Spec{}
		if err := json.Unmarshal(c.Spec.GetValue(), spec); err != nil {
			return fmt.Errorf("failed to unmarshal container OCI Spec for NRI: %w", err)
		}

		adjust, err := a.CreateContainer(ctx, c, spec)
		if err != nil {
			return fmt.Errorf("failed to get NRI adjustment for container: %w", err)
		}

		sgen := generate.Generator{Config: spec}
		ngen := nrigen.SpecGenerator(&sgen, resourceCheckOpt, rdtResolveOpt, blkioResolveOpt)

		err = ngen.Adjust(adjust)
		if err != nil {
			return fmt.Errorf("failed to NRI-adjust container Spec: %w", err)
		}

		adjusted, err := typeurl.MarshalAny(spec)
		if err != nil {
			return fmt.Errorf("failed to marshal NRI-adjusted Spec: %w", err)
		}

		c.Spec = adjusted
		return nil
	}
}

func (a *API) WithContainerExit(criCtr *cstore.Container) containerd.ProcessDeleteOpts {
	if a.IsDisabled() {
		return func(_ context.Context, _ containerd.Process) error {
			return nil
		}
	}

	return func(_ context.Context, _ containerd.Process) error {
		a.NotifyContainerExit(context.Background(), criCtr)
		return nil
	}
}

//
// NRI-CRI 'domain' interface
//
// These functions are used to interface CRI pods and containers
// from the common NRI interface. They implement pod and container
// discovery, lookup and updating of container parameters.
//

const (
	nriDomain = constants.K8sContainerdNamespace
)

func (a *API) GetName() string {
	return nriDomain
}

func (a *API) ListPodSandboxes() []nri.PodSandbox {
	pods := []nri.PodSandbox{}
	for _, pod := range a.cri.SandboxStore().List() {
		if pod.Status.Get().State != sstore.StateUnknown {
			pod := pod
			pods = append(pods, a.nriPodSandbox(&pod))
		}
	}
	return pods
}

func (a *API) ListContainers() []nri.Container {
	containers := []nri.Container{}
	for _, ctr := range a.cri.ContainerStore().List() {
		switch ctr.Status.Get().State() {
		case cri.ContainerState_CONTAINER_EXITED:
			continue
		case cri.ContainerState_CONTAINER_UNKNOWN:
			continue
		}
		ctr := ctr
		containers = append(containers, a.nriContainer(&ctr, nil))
	}
	return containers
}

func (a *API) GetPodSandbox(id string) (nri.PodSandbox, bool) {
	pod, err := a.cri.SandboxStore().Get(id)
	if err != nil {
		return nil, false
	}

	return a.nriPodSandbox(&pod), true
}

func (a *API) GetContainer(id string) (nri.Container, bool) {
	ctr, err := a.cri.ContainerStore().Get(id)
	if err != nil {
		return nil, false
	}

	return a.nriContainer(&ctr, nil), true
}

func (a *API) UpdateContainer(ctx context.Context, u *api.ContainerUpdate) error {
	ctr, err := a.cri.ContainerStore().Get(u.ContainerId)
	if err != nil {
		return nil
	}

	err = ctr.Status.UpdateSync(
		func(status cstore.Status) (cstore.Status, error) {
			criReq := &cri.UpdateContainerResourcesRequest{
				ContainerId: u.ContainerId,
				Linux:       u.GetLinux().GetResources().ToCRI(0),
			}
			newStatus, err := a.cri.UpdateContainerResources(ctx, ctr, criReq, status)
			return newStatus, err
		},
	)
	if err != nil {
		if !u.IgnoreFailure {
			return err
		}
	}

	return nil
}

func (a *API) EvictContainer(ctx context.Context, e *api.ContainerEviction) error {
	ctr, err := a.cri.ContainerStore().Get(e.ContainerId)
	if err != nil {
		return nil
	}
	err = a.cri.StopContainer(ctx, ctr, 0)
	if err != nil {
		return err
	}

	return nil
}

//
// NRI integration wrapper for CRI Pods
//

type criPodSandbox struct {
	*sstore.Sandbox
	spec *specs.Spec
	pid  uint32
}

func (a *API) nriPodSandbox(pod *sstore.Sandbox) *criPodSandbox {
	criPod := &criPodSandbox{
		Sandbox: pod,
		spec:    &specs.Spec{},
	}

	if pod == nil || pod.Container == nil {
		return criPod
	}

	ctx := ctrdutil.NamespacedContext()
	task, err := pod.Container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			log.L.WithError(err).Errorf("failed to get task for sandbox container %s",
				pod.Container.ID())
		}
		return criPod
	}

	criPod.pid = task.Pid()
	spec, err := task.Spec(ctx)
	if err != nil {
		if err != nil {
			log.L.WithError(err).Errorf("failed to get spec for sandbox container %s",
				pod.Container.ID())
		}
		return criPod
	}

	criPod.spec = spec

	return criPod
}

func (p *criPodSandbox) GetDomain() string {
	return nriDomain
}

func (p *criPodSandbox) GetID() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.ID
}

func (p *criPodSandbox) GetName() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetMetadata().GetName()
}

func (p *criPodSandbox) GetUID() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetMetadata().GetUid()
}

func (p *criPodSandbox) GetNamespace() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetMetadata().GetNamespace()
}

func (p *criPodSandbox) GetAnnotations() map[string]string {
	if p.Sandbox == nil {
		return nil
	}

	annotations := map[string]string{}

	for key, value := range p.Config.GetAnnotations() {
		annotations[key] = value
	}
	for key, value := range p.spec.Annotations {
		annotations[key] = value
	}

	return annotations
}

func (p *criPodSandbox) GetLabels() map[string]string {
	if p.Sandbox == nil {
		return nil
	}

	labels := map[string]string{}

	for key, value := range p.Config.GetLabels() {
		labels[key] = value
	}

	if p.Sandbox.Container == nil {
		return labels
	}

	ctx := ctrdutil.NamespacedContext()
	ctrd := p.Sandbox.Container
	ctrs, err := ctrd.Info(ctx, containerd.WithoutRefreshedMetadata)
	if err != nil {
		log.L.WithError(err).Errorf("failed to get info for sandbox container %s", ctrd.ID())
		return labels
	}

	for key, value := range ctrs.Labels {
		labels[key] = value
	}

	return labels
}

func (p *criPodSandbox) GetRuntimeHandler() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.RuntimeHandler
}

func (p *criPodSandbox) GetLinuxPodSandbox() nri.LinuxPodSandbox {
	return p
}

func (p *criPodSandbox) GetLinuxNamespaces() []*api.LinuxNamespace {
	if p.spec.Linux != nil {
		return api.FromOCILinuxNamespaces(p.spec.Linux.Namespaces)
	}
	return nil
}

func (p *criPodSandbox) GetPodLinuxOverhead() *api.LinuxResources {
	if p.Sandbox == nil {
		return nil
	}
	return api.FromCRILinuxResources(p.Config.GetLinux().GetOverhead())
}
func (p *criPodSandbox) GetPodLinuxResources() *api.LinuxResources {
	if p.Sandbox == nil {
		return nil
	}
	return api.FromCRILinuxResources(p.Config.GetLinux().GetResources())
}

func (p *criPodSandbox) GetLinuxResources() *api.LinuxResources {
	if p.spec.Linux == nil {
		return nil
	}
	return api.FromOCILinuxResources(p.spec.Linux.Resources, nil)
}

func (p *criPodSandbox) GetCgroupParent() string {
	if p.Sandbox == nil {
		return ""
	}
	return p.Config.GetLinux().GetCgroupParent()
}

func (p *criPodSandbox) GetCgroupsPath() string {
	if p.spec.Linux == nil {
		return ""
	}
	return p.spec.Linux.CgroupsPath
}

func (p *criPodSandbox) GetPid() uint32 {
	return p.pid
}

//
// NRI integration wrapper for CRI Containers
//

type criContainer struct {
	api  *API
	ctrs *containers.Container
	spec *specs.Spec
	meta *cstore.Metadata
	pid  uint32
}

func (a *API) nriContainer(ctr interface{}, spec *specs.Spec) *criContainer {
	switch c := ctr.(type) {
	case *cstore.Container:
		ctx := ctrdutil.NamespacedContext()
		pid := uint32(0)
		ctrd := c.Container
		ctrs, err := ctrd.Info(ctx, containerd.WithoutRefreshedMetadata)
		if err != nil {
			log.L.WithError(err).Errorf("failed to get info for container %s", ctrd.ID())
		}
		spec, err := ctrd.Spec(ctx)
		if err != nil {
			log.L.WithError(err).Errorf("failed to get OCI Spec for container %s", ctrd.ID())
			spec = &specs.Spec{}
		}
		task, err := ctrd.Task(ctx, nil)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				log.L.WithError(err).Errorf("failed to get task for container %s", ctrd.ID())
			}
		} else {
			pid = task.Pid()
		}

		return &criContainer{
			api:  a,
			ctrs: &ctrs,
			meta: &c.Metadata,
			spec: spec,
			pid:  pid,
		}

	case *containers.Container:
		ctrs := c
		meta := &cstore.Metadata{}
		if ext := ctrs.Extensions[a.cri.ContainerMetadataExtensionKey()]; ext != nil {
			err := typeurl.UnmarshalTo(ext, meta)
			if err != nil {
				log.L.WithError(err).Errorf("failed to get metadata for container %s", ctrs.ID)
			}
		}

		return &criContainer{
			api:  a,
			ctrs: ctrs,
			meta: meta,
			spec: spec,
		}
	}

	log.L.Errorf("can't wrap %T as NRI container", ctr)
	return &criContainer{
		api:  a,
		meta: &cstore.Metadata{},
		spec: &specs.Spec{},
	}
}

func (c *criContainer) GetDomain() string {
	return nriDomain
}

func (c *criContainer) GetID() string {
	if c.ctrs != nil {
		return c.ctrs.ID
	}
	return ""
}

func (c *criContainer) GetPodSandboxID() string {
	return c.spec.Annotations[annotations.SandboxID]
}

func (c *criContainer) GetName() string {
	return c.spec.Annotations[annotations.ContainerName]
}

func (c *criContainer) GetState() api.ContainerState {
	criCtr, err := c.api.cri.ContainerStore().Get(c.GetID())
	if err != nil {
		return api.ContainerState_CONTAINER_UNKNOWN
	}
	switch criCtr.Status.Get().State() {
	case cri.ContainerState_CONTAINER_CREATED:
		return api.ContainerState_CONTAINER_CREATED
	case cri.ContainerState_CONTAINER_RUNNING:
		return api.ContainerState_CONTAINER_RUNNING
	case cri.ContainerState_CONTAINER_EXITED:
		return api.ContainerState_CONTAINER_STOPPED
	}

	return api.ContainerState_CONTAINER_UNKNOWN
}

func (c *criContainer) GetLabels() map[string]string {
	if c.ctrs == nil {
		return nil
	}

	labels := map[string]string{}
	for key, value := range c.ctrs.Labels {
		labels[key] = value
	}

	if c.meta != nil && c.meta.Config != nil {
		for key, value := range c.meta.Config.Labels {
			labels[key] = value
		}
	}

	return labels
}

func (c *criContainer) GetAnnotations() map[string]string {
	annotations := map[string]string{}

	for key, value := range c.spec.Annotations {
		annotations[key] = value
	}
	if c.meta != nil && c.meta.Config != nil {
		for key, value := range c.meta.Config.Annotations {
			annotations[key] = value
		}
	}

	return annotations
}

func (c *criContainer) GetArgs() []string {
	if c.spec.Process == nil {
		return nil
	}
	return api.DupStringSlice(c.spec.Process.Args)
}

func (c *criContainer) GetEnv() []string {
	if c.spec.Process == nil {
		return nil
	}
	return api.DupStringSlice(c.spec.Process.Env)
}

func (c *criContainer) GetMounts() []*api.Mount {
	return api.FromOCIMounts(c.spec.Mounts)
}

func (c *criContainer) GetHooks() *api.Hooks {
	return api.FromOCIHooks(c.spec.Hooks)
}

func (c *criContainer) GetLinuxContainer() nri.LinuxContainer {
	return c
}

func (c *criContainer) GetLinuxNamespaces() []*api.LinuxNamespace {
	if c.spec.Linux == nil {
		return nil
	}
	return api.FromOCILinuxNamespaces(c.spec.Linux.Namespaces)
}

func (c *criContainer) GetLinuxDevices() []*api.LinuxDevice {
	if c.spec.Linux == nil {
		return nil
	}
	return api.FromOCILinuxDevices(c.spec.Linux.Devices)
}

func (c *criContainer) GetLinuxResources() *api.LinuxResources {
	if c.spec.Linux == nil {
		return nil
	}
	return api.FromOCILinuxResources(c.spec.Linux.Resources, c.spec.Annotations)
}

func (c *criContainer) GetOOMScoreAdj() *int {
	if c.spec.Process == nil {
		return nil
	}
	return c.spec.Process.OOMScoreAdj
}

func (c *criContainer) GetCgroupsPath() string {
	if c.spec.Linux == nil {
		return ""
	}
	return c.spec.Linux.CgroupsPath
}

func (c *criContainer) GetPid() uint32 {
	return c.pid
}
