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

package cc

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd"
	v1 "github.com/containerd/containerd/api/services/containers/v1"
	"github.com/containerd/containerd/api/services/diff/v1"
	"github.com/containerd/containerd/api/services/images/v1"
	introspectionapi "github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/containerd/containerd/api/services/namespaces/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/runtime/v2/task"
	"github.com/containerd/containerd/services"
	"github.com/containerd/containerd/snapshots"
	"github.com/davecgh/go-spew/spew"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/opencontainers/selinux/go-selinux/label"
	"github.com/pkg/errors"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/containerd/containerd/pkg/apparmor"
	"github.com/containerd/containerd/pkg/cap"
	"github.com/containerd/containerd/pkg/cri/annotations"
	"github.com/containerd/containerd/pkg/cri/config"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/containerd/containerd/pkg/cri/constants"
	cio "github.com/containerd/containerd/pkg/cri/io"
	customopts "github.com/containerd/containerd/pkg/cri/opts"
	"github.com/containerd/containerd/pkg/cri/server"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	cristore "github.com/containerd/containerd/pkg/cri/store/service"
	"github.com/containerd/containerd/pkg/cri/util"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	osinterface "github.com/containerd/containerd/pkg/os"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.CRIPlugin,
		ID:   "cc",
		Requires: []plugin.Type{
			plugin.CRIServicePlugin,
			plugin.RuntimePluginV2,
		},
		InitFn: initCRICCService,
	})
}

func initCRICCService(ic *plugin.InitContext) (interface{}, error) {
	servicesOpts, err := getServicesOpts(ic)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get services")
	}
	criStore, err := getCRIStore(ic)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get CRI store services")
	}
	v2r, err := ic.Get(plugin.RuntimePluginV2)
	if err != nil {
		return nil, err
	}
	client, err := containerd.New(
		"",
		containerd.WithDefaultNamespace(constants.K8sContainerdNamespace),
		containerd.WithDefaultPlatform(platforms.Default()),
		containerd.WithServices(servicesOpts...),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create containerd client")
	}
	// TODO
	allCaps, err := cap.Current()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get caps")
	}
	cc := &ccService{
		Store:     criStore,
		v2Runtime: v2r.(*v2.TaskManager),
		os:        osinterface.RealOS{},
		client:    client,
		allCaps:   allCaps,
	}
	return cc, nil
}

type ccService struct {
	// stores all resources associated with CRI
	*cristore.Store
	// config contains all configurations.
	config *criconfig.Config
	// default CRI implemention
	delegate server.GrpcServices
	// client is an instance of the containerd client
	client *containerd.Client

	// TODO: Implemented with shim Service
	v2Runtime *v2.TaskManager

	// os is an interface for all required os operations.
	os      osinterface.OS
	allCaps []string // nolint
}

func getCRIStore(ic *plugin.InitContext) (*cristore.Store, error) {
	plugins, err := ic.GetByType(plugin.CRIServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cri store")
	}
	p := plugins[cristore.CRIStoreService]
	if p == nil {
		return nil, errors.Errorf("cri service store not found")
	}
	i, err := p.Instance()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get instance of cri service store")
	}
	return i.(*cristore.Store), nil
}

// getServicesOpts get service options from plugin context.
func getServicesOpts(ic *plugin.InitContext) ([]containerd.ServicesOpt, error) {
	plugins, err := ic.GetByType(plugin.ServicePlugin)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service plugin")
	}

	opts := []containerd.ServicesOpt{
		containerd.WithEventService(ic.Events),
	}
	for s, fn := range map[string]func(interface{}) containerd.ServicesOpt{
		services.ContentService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContentStore(s.(content.Store))
		},
		services.ImagesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithImageClient(s.(images.ImagesClient))
		},
		services.SnapshotsService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithSnapshotters(s.(map[string]snapshots.Snapshotter))
		},
		services.ContainersService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithContainerClient(s.(v1.ContainersClient))
		},
		services.TasksService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithTaskClient(s.(tasks.TasksClient))
		},
		services.DiffService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithDiffClient(s.(diff.DiffClient))
		},
		services.NamespacesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithNamespaceClient(s.(namespaces.NamespacesClient))
		},
		services.LeasesService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithLeasesService(s.(leases.Manager))
		},
		services.IntrospectionService: func(s interface{}) containerd.ServicesOpt {
			return containerd.WithIntrospectionClient(s.(introspectionapi.IntrospectionClient))
		},
	} {
		p := plugins[s]
		if p == nil {
			return nil, errors.Errorf("service %q not found", s)
		}
		i, err := p.Instance()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get instance of service %q", s)
		}
		if i == nil {
			return nil, errors.Errorf("instance of service %q not found", s)
		}
		opts = append(opts, fn(i))
	}
	return opts, nil
}

func (cc *ccService) SetDelegate(delegate server.GrpcServices) {
	cc.delegate = delegate
}

func (cc *ccService) SetConfig(config *criconfig.Config) {
	cc.config = config
}

// implement CRIPlugin Initialized interface
func (cc *ccService) Initialized() bool {
	return true
}

// Run starts the CRI service.
func (cc *ccService) Run() error {
	return nil
}

// Close stops the CRI service.
func (cc *ccService) Close() error {
	return nil
}

func (cc *ccService) RunPodSandbox(ctx context.Context, r *runtime.RunPodSandboxRequest) (*runtime.RunPodSandboxResponse, error) {
	return cc.delegate.RunPodSandbox(ctx, r)
}

func (cc *ccService) ListPodSandbox(ctx context.Context, r *runtime.ListPodSandboxRequest) (*runtime.ListPodSandboxResponse, error) {
	return cc.delegate.ListPodSandbox(ctx, r)
}

func (cc *ccService) PodSandboxStatus(ctx context.Context, r *runtime.PodSandboxStatusRequest) (*runtime.PodSandboxStatusResponse, error) {
	return cc.delegate.PodSandboxStatus(ctx, r)
}

func (cc *ccService) StopPodSandbox(ctx context.Context, r *runtime.StopPodSandboxRequest) (*runtime.StopPodSandboxResponse, error) {
	return cc.delegate.StopPodSandbox(ctx, r)
}

func (cc *ccService) RemovePodSandbox(ctx context.Context, r *runtime.RemovePodSandboxRequest) (*runtime.RemovePodSandboxResponse, error) {
	return cc.delegate.RemovePodSandbox(ctx, r)
}

func (cc *ccService) PortForward(ctx context.Context, r *runtime.PortForwardRequest) (*runtime.PortForwardResponse, error) {
	return cc.delegate.PortForward(ctx, r)
}

func (cc *ccService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (_ *runtime.CreateContainerResponse, retErr error) {
	config := r.GetConfig()

	sandboxConfig := r.GetSandboxConfig()
	sandbox, err := cc.SandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find sandbox id %q", r.GetPodSandboxId())
	}
	sandboxID := sandbox.ID
	s, err := sandbox.Container.Task(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get sandbox container task")
	}
	sandboxPid := s.Pid()

	// Generate unique id and name for the container and reserve the name.
	// Reserve the container name to avoid concurrent `CreateContainer` request creating
	// the same container.
	id := util.GenerateID()
	metadata := config.GetMetadata()
	if metadata == nil {
		return nil, errors.New("container config must include metadata")
	}
	containerName := metadata.Name

	name := server.MakeContainerName(metadata, sandboxConfig.GetMetadata())
	log.G(ctx).Debugf("Generated id %q for container %q", id, name)
	if err = cc.ContainerNameIndex.Reserve(name, id); err != nil {
		return nil, errors.Wrapf(err, "failed to reserve container name %q", name)
	}
	defer func() {
		// Release the name if the function returns with an error.
		if retErr != nil {
			cc.ContainerNameIndex.ReleaseByName(name)
		}
	}()
	// Create initial internal container metadata.
	meta := containerstore.Metadata{
		ID:        id,
		Name:      name,
		SandboxID: sandboxID,
		Config:    config,
	}

	// Run container using the same runtime with sandbox.
	sandboxInfo, err := sandbox.Container.Info(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get sandbox %q info", sandboxID)
	}

	// Create container root directory.
	containerRootDir := cc.getContainerRootDir(id)
	if err = cc.os.MkdirAll(containerRootDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create container root directory %q",
			containerRootDir)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the container root directory.
			if err = cc.os.RemoveAll(containerRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove container root directory %q",
					containerRootDir)
			}
		}
	}()
	volatileContainerRootDir := cc.getVolatileContainerRootDir(id)
	if err = cc.os.MkdirAll(volatileContainerRootDir, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create volatile container root directory %q",
			volatileContainerRootDir)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the volatile container root directory.
			if err = cc.os.RemoveAll(volatileContainerRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove volatile container root directory %q",
					volatileContainerRootDir)
			}
		}
	}()

	var volumeMounts []*runtime.Mount

	// Generate container mounts.
	mounts := cc.containerMounts(sandboxID, config)

	ociRuntime, err := cc.getSandboxRuntime(sandboxConfig, sandbox.Metadata.RuntimeHandler)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get sandbox runtime")
	}
	log.G(ctx).Debugf("====> Use OCI runtime %+v for sandbox %q and container %q", ociRuntime, sandboxID, id)

	spec, err := cc.containerSpec(id, sandboxID, sandboxPid, sandbox.NetNSPath, containerName, config.Image.Image, config, sandboxConfig,
		append(mounts, volumeMounts...), ociRuntime)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to generate container %q spec", id)
	}

	meta.ProcessLabel = spec.Process.SelinuxLabel

	// handle any KVM based runtime
	if err := server.ModifyProcessLabel(ociRuntime.Type, spec); err != nil {
		return nil, err
	}

	if config.GetLinux().GetSecurityContext().GetPrivileged() {
		// If privileged don't set the SELinux label but still record it on the container so
		// the unused MCS label can be release later
		spec.Process.SelinuxLabel = ""
	}
	defer func() {
		if retErr != nil {
			selinux.ReleaseLabel(spec.Process.SelinuxLabel)
		}
	}()

	log.G(ctx).Debugf("Container %q spec: %#+v", id, spew.NewFormatter(spec))

	opts := []containerd.NewContainerOpts{}
	//meta.ImageRef = image.ID
	//meta.StopSignal = image.ImageSpec.Config.StopSignal

	// Validate log paths and compose full container log path.
	if sandboxConfig.GetLogDirectory() != "" && config.GetLogPath() != "" {
		meta.LogPath = filepath.Join(sandboxConfig.GetLogDirectory(), config.GetLogPath())
		log.G(ctx).Debugf("Composed container full log path %q using sandbox log dir %q and container log path %q",
			meta.LogPath, sandboxConfig.GetLogDirectory(), config.GetLogPath())
	} else {
		log.G(ctx).Infof("Logging will be disabled due to empty log paths for sandbox (%q) or container (%q)",
			sandboxConfig.GetLogDirectory(), config.GetLogPath())
	}

	containerIO, err := cio.NewContainerIO(id,
		cio.WithNewFIFOs(volatileContainerRootDir, config.GetTty(), config.GetStdin()))
	if err != nil {
		return nil, errors.Wrap(err, "failed to create container io")
	}
	defer func() {
		if retErr != nil {
			if err := containerIO.Close(); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to close container io %q", id)
			}
		}
	}()

	specOpts, err := cc.containerSpecOpts(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get container spec opts")
	}

	const containerKindContainer = "container"
	containerLabels := server.BuildLabels(config.Labels, containerKindContainer)

	runtimeOptions, err := server.GetRuntimeOptions(sandboxInfo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get runtime options")
	}

	const criContainerdPrefix = "io.cri-containerd"
	const containerMetadataExtension = criContainerdPrefix + ".container.metadata"
	opts = append(opts,
		containerd.WithSpec(spec, specOpts...),
		containerd.WithRuntime(sandboxInfo.Runtime.Name, runtimeOptions),
		containerd.WithContainerLabels(containerLabels),
		containerd.WithContainerExtension(containerMetadataExtension, &meta))
	var cntr containerd.Container
	if cntr, err = cc.client.NewContainer(ctx, id, opts...); err != nil {
		return nil, errors.Wrap(err, "failed to create containerd container")
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := ctrdutil.DeferContext()
			defer deferCancel()
			if err := cntr.Delete(deferCtx, containerd.WithSnapshotCleanup); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to delete containerd container %q", id)
			}
		}
	}()

	status := containerstore.Status{CreatedAt: time.Now().UnixNano()}
	container, err := containerstore.NewContainer(meta,
		containerstore.WithStatus(status, containerRootDir),
		containerstore.WithContainer(cntr),
		containerstore.WithContainerIO(containerIO),
	)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create internal container object for %q", id)
	}
	defer func() {
		if retErr != nil {
			// Cleanup container checkpoint on error.
			if err := container.Delete(); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to cleanup container checkpoint for %q", id)
			}
		}
	}()

	// Add container into container store.
	if err := cc.ContainerStore.Add(container); err != nil {
		return nil, errors.Wrapf(err, "failed to add container %q into store", id)
	}

	return &runtime.CreateContainerResponse{ContainerId: id}, nil
	//return cc.delegate.CreateContainer(ctx, r)
}

// getSandboxRootDir returns the root directory for managing sandbox files,
// e.g. hosts files.
func (cc *ccService) getSandboxRootDir(id string) string {
	return filepath.Join(cc.config.RootDir, "sandboxes", id)
}

// getVolatileSandboxRootDir returns the root directory for managing volatile sandbox files,
// e.g. named pipes.
func (cc *ccService) getVolatileSandboxRootDir(id string) string {
	return filepath.Join(cc.config.StateDir, "sandboxes", id)
}

// getSandboxHostname returns the hostname file path inside the sandbox root directory.
func (cc *ccService) getSandboxHostname(id string) string {
	return filepath.Join(cc.getSandboxRootDir(id), "hostname")
}

// getSandboxHosts returns the hosts file path inside the sandbox root directory.
func (cc *ccService) getSandboxHosts(id string) string {
	return filepath.Join(cc.getSandboxRootDir(id), "hosts")
}

// getResolvPath returns resolv.conf filepath for specified sandbox.
func (cc *ccService) getResolvPath(id string) string {
	return filepath.Join(cc.getSandboxRootDir(id), "resolv.conf")
}

// getSandboxDevShm returns the shm file path inside the sandbox root directory.
func (cc *ccService) getSandboxDevShm(id string) string {
	return filepath.Join(cc.getVolatileSandboxRootDir(id), "shm")
}

// getContainerRootDir returns the root directory for managing container files,
// e.g. state checkpoint.
func (cc *ccService) getContainerRootDir(id string) string {
	return filepath.Join(cc.config.RootDir, "containers", id)
}

// getVolatileContainerRootDir returns the root directory for managing volatile container files,
// e.g. named pipes.
func (cc *ccService) getVolatileContainerRootDir(id string) string {
	return filepath.Join(cc.config.StateDir, "containers", id)
}

const (
	etcHostname    = "/etc/hostname"
	etcHosts       = "/etc/hosts"
	resolvConfPath = "/etc/resolv.conf"
	devShm         = "/dev/shm"
)

// containerMounts sets up necessary container system file mounts
// including /dev/shm, /etc/hosts and /etc/resolv.conf.
func (cc *ccService) containerMounts(sandboxID string, config *runtime.ContainerConfig) []*runtime.Mount {
	var mounts []*runtime.Mount
	securityContext := config.GetLinux().GetSecurityContext()
	if !server.IsInCRIMounts(etcHostname, config.GetMounts()) {
		// /etc/hostname is added since 1.1.6, 1.2.4 and 1.3.
		// For in-place upgrade, the old sandbox doesn't have the hostname file,
		// do not mount this in that case.
		// TODO(random-liu): Remove the check and always mount this when
		// containerd 1.1 and 1.2 are deprecated.
		hostpath := cc.getSandboxHostname(sandboxID)
		if _, err := cc.os.Stat(hostpath); err == nil {
			mounts = append(mounts, &runtime.Mount{
				ContainerPath: etcHostname,
				HostPath:      hostpath,
				Readonly:      securityContext.GetReadonlyRootfs(),
			})
		}
	}

	if !server.IsInCRIMounts(etcHosts, config.GetMounts()) {
		mounts = append(mounts, &runtime.Mount{
			ContainerPath: etcHosts,
			HostPath:      cc.getSandboxHosts(sandboxID),
			Readonly:      securityContext.GetReadonlyRootfs(),
		})
	}

	// Mount sandbox resolv.config.
	// TODO: Need to figure out whether we should always mount it as read-only
	if !server.IsInCRIMounts(resolvConfPath, config.GetMounts()) {
		mounts = append(mounts, &runtime.Mount{
			ContainerPath: resolvConfPath,
			HostPath:      cc.getResolvPath(sandboxID),
			Readonly:      securityContext.GetReadonlyRootfs(),
		})
	}

	if !server.IsInCRIMounts(devShm, config.GetMounts()) {
		sandboxDevShm := cc.getSandboxDevShm(sandboxID)
		if securityContext.GetNamespaceOptions().GetIpc() == runtime.NamespaceMode_NODE {
			sandboxDevShm = devShm
		}
		mounts = append(mounts, &runtime.Mount{
			ContainerPath:  devShm,
			HostPath:       sandboxDevShm,
			Readonly:       false,
			SelinuxRelabel: sandboxDevShm != devShm,
		})
	}
	return mounts
}

func (cc *ccService) getSandboxRuntime(config *runtime.PodSandboxConfig, runtimeHandler string) (criconfig.Runtime, error) {
	handler, ok := cc.config.ContainerdConfig.Runtimes[runtimeHandler]
	if !ok {
		return criconfig.Runtime{}, errors.Errorf("no runtime for %q is configured", runtimeHandler)
	}
	return handler, nil
}
func (cc *ccService) containerSpec(
	id string,
	sandboxID string,
	sandboxPid uint32,
	netNSPath string,
	containerName string,
	imageName string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	extraMounts []*runtime.Mount,
	ociRuntime config.Runtime,
) (_ *runtimespec.Spec, retErr error) {
	specOpts := []oci.SpecOpts{
		oci.WithoutRunMount,
	}
	// only clear the default security settings if the runtime does not have a custom
	// base runtime spec spec.  Admins can use this functionality to define
	// default ulimits, seccomp, or other default settings.
	if ociRuntime.BaseRuntimeSpec == "" {
		specOpts = append(specOpts, customopts.WithoutDefaultSecuritySettings)
	}
	relativeRootfsPath := filepath.Join(cc.getContainerRootDir(id), "rootfs")
	specOpts = append(specOpts,
		customopts.WithRelativeRoot(relativeRootfsPath),
		WithProcessArgs(config),
		oci.WithDefaultPathEnv,
		// this will be set based on the security context below
		oci.WithNewPrivileges,
	)
	if config.GetWorkingDir() != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(config.GetWorkingDir()))
	}

	if config.GetTty() {
		specOpts = append(specOpts, oci.WithTTY)
	}
	// Add HOSTNAME env.
	var (
		err      error
		hostname = sandboxConfig.GetHostname()
	)
	if hostname == "" {
		if hostname, err = cc.os.Hostname(); err != nil {
			return nil, err
		}
	}
	const hostnameEnv = "HOSTNAME"
	specOpts = append(specOpts, oci.WithEnv([]string{hostnameEnv + "=" + hostname}))

	// Apply envs from image config first, so that envs from container config
	// can override them.
	env := []string{}
	for _, e := range config.GetEnvs() {
		env = append(env, e.GetKey()+"="+e.GetValue())
	}
	specOpts = append(specOpts, oci.WithEnv(env))

	securityContext := config.GetLinux().GetSecurityContext()
	labelOptions, err := server.ToLabel(securityContext.GetSelinuxOptions())
	if err != nil {
		return nil, err
	}
	if len(labelOptions) == 0 {
		// Use pod level SELinux config
		if sandbox, err := cc.SandboxStore.Get(sandboxID); err == nil {
			labelOptions, err = selinux.DupSecOpt(sandbox.Metadata.ProcessLabel)
			if err != nil {
				return nil, err
			}
		}
	}
	processLabel, mountLabel, err := label.InitLabels(labelOptions)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to init selinux options %+v", securityContext.GetSelinuxOptions())
	}
	defer func() {
		if retErr != nil {
			_ = label.ReleaseLabel(processLabel)
		}
	}()

	specOpts = append(specOpts, customopts.WithMounts(cc.os, config, extraMounts, mountLabel), customopts.WithRelabeledContainerMounts(mountLabel))

	if !cc.config.DisableProcMount {
		// Change the default masked/readonly paths to empty slices
		// See https://github.com/containerd/containerd/issues/5029
		// TODO: Provide an option to set default paths to the ones in oci.populateDefaultUnixSpec()
		specOpts = append(specOpts, oci.WithMaskedPaths([]string{}), oci.WithReadonlyPaths([]string{}))

		// Apply masked paths if specified.
		// If the container is privileged, this will be cleared later on.
		if maskedPaths := securityContext.GetMaskedPaths(); maskedPaths != nil {
			specOpts = append(specOpts, oci.WithMaskedPaths(maskedPaths))
		}

		// Apply readonly paths if specified.
		// If the container is privileged, this will be cleared later on.
		if readonlyPaths := securityContext.GetReadonlyPaths(); readonlyPaths != nil {
			specOpts = append(specOpts, oci.WithReadonlyPaths(readonlyPaths))
		}
	}

	specOpts = append(specOpts, customopts.WithDevices(cc.os, config, cc.config.DeviceOwnershipFromSecurityContext),
		customopts.WithCapabilities(securityContext, cc.allCaps))

	if securityContext.GetPrivileged() {
		if !sandboxConfig.GetLinux().GetSecurityContext().GetPrivileged() {
			return nil, errors.New("no privileged container allowed in sandbox")
		}
		specOpts = append(specOpts, oci.WithPrivileged)
		if !ociRuntime.PrivilegedWithoutHostDevices {
			specOpts = append(specOpts, oci.WithHostDevices, oci.WithAllDevicesAllowed)
		}
	}

	// Clear all ambient capabilities. The implication of non-root + caps
	// is not clearly defined in Kubernetes.
	// See https://github.com/kubernetes/kubernetes/issues/56374
	// Keep docker's behavior for now.
	specOpts = append(specOpts,
		customopts.WithoutAmbientCaps,
		customopts.WithSelinuxLabels(processLabel, mountLabel),
	)

	// TODO: Figure out whether we should set no new privilege for sandbox container by default
	if securityContext.GetNoNewPrivs() {
		specOpts = append(specOpts, oci.WithNoNewPrivileges)
	}
	// TODO(random-liu): [P1] Set selinux options (privileged or not).
	if securityContext.GetReadonlyRootfs() {
		specOpts = append(specOpts, oci.WithRootFSReadonly())
	}

	if cc.config.DisableCgroup {
		specOpts = append(specOpts, customopts.WithDisabledCgroups)
	} else {
		specOpts = append(specOpts, customopts.WithResources(config.GetLinux().GetResources(), cc.config.TolerateMissingHugetlbController, cc.config.DisableHugetlbController))
		if sandboxConfig.GetLinux().GetCgroupParent() != "" {
			cgroupsPath := server.GetCgroupsPath(sandboxConfig.GetLinux().GetCgroupParent(), id)
			specOpts = append(specOpts, oci.WithCgroup(cgroupsPath))
		}
	}

	supplementalGroups := securityContext.GetSupplementalGroups()

	for pKey, pValue := range server.GetPassthroughAnnotations(sandboxConfig.Annotations,
		ociRuntime.PodAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	for pKey, pValue := range server.GetPassthroughAnnotations(config.Annotations,
		ociRuntime.ContainerAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	// Default target PID namespace is the sandbox PID.
	targetPid := sandboxPid
	// If the container targets another container's PID namespace,
	// set targetPid to the PID of that container.
	nsOpts := securityContext.GetNamespaceOptions()
	if nsOpts.GetPid() == runtime.NamespaceMode_TARGET {
		targetContainer, err := cc.validateTargetContainer(sandboxID, nsOpts.TargetId)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid target container")
		}

		status := targetContainer.Status.Get()
		targetPid = status.Pid
	}

	specOpts = append(specOpts,
		customopts.WithOOMScoreAdj(config, cc.config.RestrictOOMScoreAdj),
		customopts.WithPodNamespaces(securityContext, sandboxPid, targetPid),
		customopts.WithSupplementalGroups(supplementalGroups),
		customopts.WithAnnotation(annotations.ContainerType, annotations.ContainerTypeContainer),
		customopts.WithAnnotation(annotations.SandboxID, sandboxID),
		customopts.WithAnnotation(annotations.SandboxNamespace, sandboxConfig.GetMetadata().GetNamespace()),
		customopts.WithAnnotation(annotations.SandboxName, sandboxConfig.GetMetadata().GetName()),
		customopts.WithAnnotation(annotations.ContainerName, containerName),
		customopts.WithAnnotation(annotations.ImageName, imageName),
	)
	// cgroupns is used for hiding /sys/fs/cgroup from containers.
	// For compatibility, cgroupns is not used when running in cgroup v1 mode or in privileged.
	// https://github.com/containers/libpod/issues/4363
	// https://github.com/kubernetes/enhancements/blob/0e409b47497e398b369c281074485c8de129694f/keps/sig-node/20191118-cgroups-v2.md#cgroup-namespace
	if cgroups.Mode() == cgroups.Unified && !securityContext.GetPrivileged() {
		specOpts = append(specOpts, oci.WithLinuxNamespace(
			runtimespec.LinuxNamespace{
				Type: runtimespec.CgroupNamespace,
			}))
	}
	return cc.runtimeSpec(id, ociRuntime.BaseRuntimeSpec, specOpts...)
}

func WithProcessArgs(config *runtime.ContainerConfig) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		command, args := config.GetCommand(), config.GetArgs()

		// image.Cmd
		// image.Entrypoint

		return oci.WithProcessArgs(append(command, args...)...)(ctx, client, c, s)
	}
}

// validateTargetContainer checks that a container is a valid
// target for a container using PID NamespaceMode_TARGET.
// The target container must be in the same sandbox and must be running.
// Returns the target container for convenience.
func (cc *ccService) validateTargetContainer(sandboxID, targetContainerID string) (containerstore.Container, error) {
	targetContainer, err := cc.ContainerStore.Get(targetContainerID)
	if err != nil {
		return containerstore.Container{}, errors.Wrapf(err, "container %q does not exist", targetContainerID)
	}

	targetSandboxID := targetContainer.Metadata.SandboxID
	if targetSandboxID != sandboxID {
		return containerstore.Container{},
			errors.Errorf("container %q (sandbox %s) does not belong to sandbox %s", targetContainerID, targetSandboxID, sandboxID)
	}

	status := targetContainer.Status.Get()
	if state := status.State(); state != runtime.ContainerState_CONTAINER_RUNNING {
		return containerstore.Container{}, errors.Errorf("container %q is not running - in state %s", targetContainerID, state)
	}

	return targetContainer, nil
}

// runtimeSpec returns a default runtime spec used in cri-containerd.
func (cc *ccService) runtimeSpec(id string, baseSpecFile string, opts ...oci.SpecOpts) (*runtimespec.Spec, error) {
	// GenerateSpec needs namespace.
	ctx := ctrdutil.NamespacedContext()
	container := &containers.Container{ID: id}

	//if baseSpecFile != "" {
	//	baseSpec, ok := c.baseOCISpecs[baseSpecFile]
	//	if !ok {
	//		return nil, errors.Errorf("can't find base OCI spec %q", baseSpecFile)
	//	}

	//	spec := oci.Spec{}
	//	if err := util.DeepCopy(&spec, &baseSpec); err != nil {
	//		return nil, errors.Wrap(err, "failed to clone OCI spec")
	//	}

	//	// Fix up cgroups path
	//	applyOpts := append([]oci.SpecOpts{oci.WithNamespacedCgroup()}, opts...)

	//	if err := oci.ApplyOpts(ctx, nil, container, &spec, applyOpts...); err != nil {
	//		return nil, errors.Wrap(err, "failed to apply OCI options")
	//	}

	//	return &spec, nil
	//}

	spec, err := oci.GenerateSpec(ctx, nil, container, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate spec")
	}

	return spec, nil
}

func (cc *ccService) containerSpecOpts(config *runtime.ContainerConfig) ([]oci.SpecOpts, error) {
	var specOpts []oci.SpecOpts
	securityContext := config.GetLinux().GetSecurityContext()
	// Set container username. This could only be done by containerd, because it needs
	// access to the container rootfs. Pass user name to containerd, and let it overwrite
	// the spec for us.
	userstr, err := server.GenerateUserString(
		securityContext.GetRunAsUsername(),
		securityContext.GetRunAsUser(),
		securityContext.GetRunAsGroup())
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate user string")
	}
	if userstr != "" {
		specOpts = append(specOpts, oci.WithUser(userstr))
	}

	if securityContext.GetRunAsUsername() != "" {
		userstr = securityContext.GetRunAsUsername()
	} else {
		// Even if RunAsUser is not set, we still call `GetValue` to get uid 0.
		// Because it is still useful to get additional gids for uid 0.
		userstr = strconv.FormatInt(securityContext.GetRunAsUser().GetValue(), 10)
	}
	specOpts = append(specOpts, customopts.WithAdditionalGIDs(userstr))

	asp := securityContext.GetApparmor()
	if asp == nil {
		asp, err = server.GenerateApparmorSecurityProfile(securityContext.GetApparmorProfile()) //nolint:staticcheck // Deprecated but we don't want to remove yet
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate apparmor spec opts")
		}
	}
	apparmorSpecOpts, err := server.GenerateApparmorSpecOpts(
		asp,
		securityContext.GetPrivileged(),
		cc.apparmorEnabled())
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate apparmor spec opts")
	}
	if apparmorSpecOpts != nil {
		specOpts = append(specOpts, apparmorSpecOpts)
	}
	ssp := securityContext.GetSeccomp()
	if ssp == nil {
		ssp, err = server.GenerateSeccompSecurityProfile(
			securityContext.GetSeccompProfilePath(), //nolint:staticcheck // Deprecated but we don't want to remove yet
			cc.config.UnsetSeccompProfile)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate seccomp spec opts")
		}
	}
	seccompSpecOpts, err := server.GenerateSeccompSpecOpts(
		ssp,
		securityContext.GetPrivileged(),
		server.SeccompEnabled())
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate seccomp spec opts")
	}
	if seccompSpecOpts != nil {
		specOpts = append(specOpts, seccompSpecOpts)
	}
	return specOpts, nil
}

// apparmorEnabled returns true if apparmor is enabled, supported by the host,
// if apparmor_parser is installed, and if we are not running docker-in-docker.
func (cc *ccService) apparmorEnabled() bool {
	if cc.config.DisableApparmor {
		return false
	}
	return apparmor.HostSupports()
}

func (cc *ccService) StartContainer(ctx context.Context, r *runtime.StartContainerRequest) (*runtime.StartContainerResponse, error) {
	return cc.delegate.StartContainer(ctx, r)
}

func (cc *ccService) ListContainers(ctx context.Context, r *runtime.ListContainersRequest) (*runtime.ListContainersResponse, error) {
	return cc.delegate.ListContainers(ctx, r)
}

func (cc *ccService) ContainerStatus(ctx context.Context, r *runtime.ContainerStatusRequest) (*runtime.ContainerStatusResponse, error) {
	return cc.delegate.ContainerStatus(ctx, r)
}

func (cc *ccService) StopContainer(ctx context.Context, r *runtime.StopContainerRequest) (*runtime.StopContainerResponse, error) {
	return cc.delegate.StopContainer(ctx, r)
}

func (cc *ccService) RemoveContainer(ctx context.Context, r *runtime.RemoveContainerRequest) (*runtime.RemoveContainerResponse, error) {
	return cc.delegate.RemoveContainer(ctx, r)
}

func (cc *ccService) ExecSync(ctx context.Context, r *runtime.ExecSyncRequest) (*runtime.ExecSyncResponse, error) {
	return cc.delegate.ExecSync(ctx, r)
}

func (cc *ccService) Exec(ctx context.Context, r *runtime.ExecRequest) (*runtime.ExecResponse, error) {
	return cc.delegate.Exec(ctx, r)
}

func (cc *ccService) Attach(ctx context.Context, r *runtime.AttachRequest) (*runtime.AttachResponse, error) {
	return cc.delegate.Attach(ctx, r)
}

func (cc *ccService) UpdateContainerResources(ctx context.Context, r *runtime.UpdateContainerResourcesRequest) (*runtime.UpdateContainerResourcesResponse, error) {
	return cc.delegate.UpdateContainerResources(ctx, r)
}

func (cc *ccService) PullImage(ctx context.Context, r *runtime.PullImageRequest) (*runtime.PullImageResponse, error) {
	name := server.MakeSandboxName(r.SandboxConfig.GetMetadata())
	sandboxID := cc.SandboxNameIndex.GetKeyByName(name)

	req := &task.PullImageRequest{}
	if r.Image != nil {
		req.Image = &task.ImageSpec{
			Image:       r.Image.Image,
			Annotations: r.Image.Annotations,
		}
	}
	if r.Auth != nil {
		req.Auth = &task.AuthConfig{
			Username:      r.Auth.Username,
			Password:      r.Auth.Password,
			Auth:          r.Auth.Auth,
			ServerAddress: r.Auth.ServerAddress,
			IdentityToken: r.Auth.IdentityToken,
			RegistryToken: r.Auth.RegistryToken,
		}
	}
	if r.SandboxConfig != nil {
		req.SandboxConfig = &task.PodSandboxConfig{
			Hostname:     r.SandboxConfig.Hostname,
			LogDirectory: r.SandboxConfig.LogDirectory,
			Labels:       r.SandboxConfig.Labels,
			Annotations:  r.SandboxConfig.Annotations,
		}
		portMappings := []*task.PortMapping{}
		for _, pm := range r.SandboxConfig.PortMappings {
			portMappings = append(portMappings, &task.PortMapping{
				Protocol:      task.Protocol(pm.Protocol),
				ContainerPort: pm.ContainerPort,
				HostPort:      pm.HostPort,
				HostIp:        pm.HostIp,
			})
		}
		req.SandboxConfig.PortMappings = portMappings
		if r.SandboxConfig.Metadata != nil {
			req.SandboxConfig.Metadata = &task.PodSandboxMetadata{
				Name:      r.SandboxConfig.Metadata.Name,
				Uid:       r.SandboxConfig.Metadata.Uid,
				Namespace: r.SandboxConfig.Metadata.Namespace,
				Attempt:   r.SandboxConfig.Metadata.Attempt,
			}
		}
		if r.SandboxConfig.Linux != nil {
			req.SandboxConfig.Linux = &task.LinuxPodSandboxConfig{
				CgroupParent: r.SandboxConfig.Linux.CgroupParent,
				Sysctls:      r.SandboxConfig.Linux.Sysctls,
			}
			if r.SandboxConfig.Linux.SecurityContext != nil {
				req.SandboxConfig.Linux.SecurityContext = &task.LinuxSandboxSecurityContext{
					ReadonlyRootfs:     r.SandboxConfig.Linux.SecurityContext.ReadonlyRootfs,
					SupplementalGroups: r.SandboxConfig.Linux.SecurityContext.SupplementalGroups,
					Privileged:         r.SandboxConfig.Linux.SecurityContext.Privileged,
					SeccompProfilePath: r.SandboxConfig.Linux.SecurityContext.SeccompProfilePath,
				}
				if r.SandboxConfig.Linux.SecurityContext.NamespaceOptions != nil {
					req.SandboxConfig.Linux.SecurityContext.NamespaceOptions = &task.NamespaceOption{
						Network:  task.NamespaceMode(r.SandboxConfig.Linux.SecurityContext.NamespaceOptions.Network),
						Pid:      task.NamespaceMode(r.SandboxConfig.Linux.SecurityContext.NamespaceOptions.Pid),
						Ipc:      task.NamespaceMode(r.SandboxConfig.Linux.SecurityContext.NamespaceOptions.Ipc),
						TargetId: r.SandboxConfig.Linux.SecurityContext.NamespaceOptions.TargetId,
					}
				}
				if r.SandboxConfig.Linux.SecurityContext.SelinuxOptions != nil {
					req.SandboxConfig.Linux.SecurityContext.SelinuxOptions = &task.SELinuxOption{
						User:  r.SandboxConfig.Linux.SecurityContext.SelinuxOptions.User,
						Role:  r.SandboxConfig.Linux.SecurityContext.SelinuxOptions.Role,
						Type:  r.SandboxConfig.Linux.SecurityContext.SelinuxOptions.Type,
						Level: r.SandboxConfig.Linux.SecurityContext.SelinuxOptions.Level,
					}
				}
				if r.SandboxConfig.Linux.SecurityContext.RunAsUser != nil {
					req.SandboxConfig.Linux.SecurityContext.RunAsUser = &task.Int64Value{
						Value: r.SandboxConfig.Linux.SecurityContext.RunAsUser.Value,
					}
				}
				if r.SandboxConfig.Linux.SecurityContext.RunAsGroup != nil {
					req.SandboxConfig.Linux.SecurityContext.RunAsGroup = &task.Int64Value{
						Value: r.SandboxConfig.Linux.SecurityContext.RunAsGroup.Value,
					}
				}

				if r.SandboxConfig.Linux.SecurityContext.Seccomp != nil {
					req.SandboxConfig.Linux.SecurityContext.Seccomp = &task.SecurityProfile{
						ProfileType:  task.SecurityProfile_ProfileType(r.SandboxConfig.Linux.SecurityContext.Seccomp.ProfileType),
						LocalhostRef: r.SandboxConfig.Linux.SecurityContext.Seccomp.LocalhostRef,
					}
				}
				if r.SandboxConfig.Linux.SecurityContext.Apparmor != nil {
					req.SandboxConfig.Linux.SecurityContext.Apparmor = &task.SecurityProfile{
						ProfileType:  task.SecurityProfile_ProfileType(r.SandboxConfig.Linux.SecurityContext.Apparmor.ProfileType),
						LocalhostRef: r.SandboxConfig.Linux.SecurityContext.Apparmor.LocalhostRef,
					}
				}
			}
		}
	}
	resp, err := cc.v2Runtime.PullImage(ctx, sandboxID, req)
	if resp != nil {
		ret := &runtime.PullImageResponse{
			ImageRef: resp.ImageRef,
		}
		return ret, err
	}
	return nil, err
}

func (cc *ccService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	return cc.delegate.ListImages(ctx, r)
}

func (cc *ccService) ImageStatus(ctx context.Context, r *runtime.ImageStatusRequest) (*runtime.ImageStatusResponse, error) {
	return cc.delegate.ImageStatus(ctx, r)
}

func (cc *ccService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	return cc.delegate.RemoveImage(ctx, r)
}

func (cc *ccService) ImageFsInfo(ctx context.Context, r *runtime.ImageFsInfoRequest) (*runtime.ImageFsInfoResponse, error) {
	return cc.delegate.ImageFsInfo(ctx, r)
}

func (cc *ccService) ContainerStats(ctx context.Context, r *runtime.ContainerStatsRequest) (*runtime.ContainerStatsResponse, error) {
	return cc.delegate.ContainerStats(ctx, r)
}

func (cc *ccService) ListContainerStats(ctx context.Context, r *runtime.ListContainerStatsRequest) (*runtime.ListContainerStatsResponse, error) {
	return cc.delegate.ListContainerStats(ctx, r)
}

func (cc *ccService) Status(ctx context.Context, r *runtime.StatusRequest) (*runtime.StatusResponse, error) {
	return cc.delegate.Status(ctx, r)
}

func (cc *ccService) Version(ctx context.Context, r *runtime.VersionRequest) (*runtime.VersionResponse, error) {
	return cc.delegate.Version(ctx, r)
}

func (cc *ccService) UpdateRuntimeConfig(ctx context.Context, r *runtime.UpdateRuntimeConfigRequest) (*runtime.UpdateRuntimeConfigResponse, error) {
	return cc.delegate.UpdateRuntimeConfig(ctx, r)
}

func (cc *ccService) ReopenContainerLog(ctx context.Context, r *runtime.ReopenContainerLogRequest) (*runtime.ReopenContainerLogResponse, error) {
	return cc.delegate.ReopenContainerLog(ctx, r)
}
