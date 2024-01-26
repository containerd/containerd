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
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	"github.com/davecgh/go-spew/spew"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/selinux/go-selinux"
	"github.com/opencontainers/selinux/go-selinux/label"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/oci"
	"github.com/containerd/containerd/v2/pkg/blockio"
	"github.com/containerd/containerd/v2/pkg/cri/annotations"
	criconfig "github.com/containerd/containerd/v2/pkg/cri/config"
	cio "github.com/containerd/containerd/v2/pkg/cri/io"
	crilabels "github.com/containerd/containerd/v2/pkg/cri/labels"
	customopts "github.com/containerd/containerd/v2/pkg/cri/opts"
	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	"github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/platforms"
)

func init() {
	typeurl.Register(&containerstore.Metadata{},
		"github.com/containerd/cri/pkg/store/container", "Metadata")
}

// CreateContainer creates a new container in the given PodSandbox.
func (c *criService) CreateContainer(ctx context.Context, r *runtime.CreateContainerRequest) (_ *runtime.CreateContainerResponse, retErr error) {
	config := r.GetConfig()
	log.G(ctx).Debugf("Container config %+v", config)
	sandboxConfig := r.GetSandboxConfig()
	sandbox, err := c.sandboxStore.Get(r.GetPodSandboxId())
	if err != nil {
		return nil, fmt.Errorf("failed to find sandbox id %q: %w", r.GetPodSandboxId(), err)
	}

	controller, err := c.sandboxService.SandboxController(sandbox.Config, sandbox.RuntimeHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox controller: %w", err)
	}

	cstatus, err := controller.Status(ctx, sandbox.ID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get controller status: %w", err)
	}

	var (
		sandboxID  = cstatus.SandboxID
		sandboxPid = cstatus.Pid
	)

	// Generate unique id and name for the container and reserve the name.
	// Reserve the container name to avoid concurrent `CreateContainer` request creating
	// the same container.
	id := util.GenerateID()
	metadata := config.GetMetadata()
	if metadata == nil {
		return nil, errors.New("container config must include metadata")
	}
	containerName := metadata.Name
	name := makeContainerName(metadata, sandboxConfig.GetMetadata())
	log.G(ctx).Debugf("Generated id %q for container %q", id, name)
	if err = c.containerNameIndex.Reserve(name, id); err != nil {
		return nil, fmt.Errorf("failed to reserve container name %q: %w", name, err)
	}
	defer func() {
		// Release the name if the function returns with an error.
		if retErr != nil {
			c.containerNameIndex.ReleaseByName(name)
		}
	}()

	// Create initial internal container metadata.
	meta := containerstore.Metadata{
		ID:        id,
		Name:      name,
		SandboxID: sandboxID,
		Config:    config,
	}

	// Prepare container image snapshot. For container, the image should have
	// been pulled before creating the container, so do not ensure the image.
	image, err := c.LocalResolve(config.GetImage().GetImage())
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image %q: %w", config.GetImage().GetImage(), err)
	}
	containerdImage, err := c.toContainerdImage(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("failed to get image from containerd %q: %w", image.ID, err)
	}

	start := time.Now()

	// Create container root directory.
	containerRootDir := c.getContainerRootDir(id)
	if err = c.os.MkdirAll(containerRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create container root directory %q: %w",
			containerRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the container root directory.
			if err = c.os.RemoveAll(containerRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove container root directory %q",
					containerRootDir)
			}
		}
	}()
	volatileContainerRootDir := c.getVolatileContainerRootDir(id)
	if err = c.os.MkdirAll(volatileContainerRootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create volatile container root directory %q: %w",
			volatileContainerRootDir, err)
	}
	defer func() {
		if retErr != nil {
			// Cleanup the volatile container root directory.
			if err = c.os.RemoveAll(volatileContainerRootDir); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to remove volatile container root directory %q",
					volatileContainerRootDir)
			}
		}
	}()

	platform, err := controller.Platform(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to query sandbox platform: %w", err)
	}

	var volumeMounts []*runtime.Mount
	if !c.config.IgnoreImageDefinedVolumes {
		// Create container image volumes mounts.
		volumeMounts = c.volumeMounts(platform, containerRootDir, config, &image.ImageSpec.Config)
	} else if len(image.ImageSpec.Config.Volumes) != 0 {
		log.G(ctx).Debugf("Ignoring volumes defined in image %v because IgnoreImageDefinedVolumes is set", image.ID)
	}

	ociRuntime, err := c.config.GetSandboxRuntime(sandboxConfig, sandbox.Metadata.RuntimeHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox runtime: %w", err)
	}
	log.G(ctx).Debugf("Use OCI runtime %+v for sandbox %q and container %q", ociRuntime, sandboxID, id)

	spec, err := c.buildContainerSpec(
		platform,
		id,
		sandboxID,
		sandboxPid,
		sandbox.NetNSPath,
		containerName,
		containerdImage.Name(),
		config,
		sandboxConfig,
		&image.ImageSpec.Config,
		volumeMounts,
		ociRuntime,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate container %q spec: %w", id, err)
	}

	meta.ProcessLabel = spec.Process.SelinuxLabel

	// handle any KVM based runtime
	if err := modifyProcessLabel(ociRuntime.Type, spec); err != nil {
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

	// Grab any platform specific snapshotter opts.
	sOpts, err := snapshotterOpts(c.config.ContainerdConfig.Snapshotter, config)
	if err != nil {
		return nil, err
	}

	// Set snapshotter before any other options.
	opts := []containerd.NewContainerOpts{
		containerd.WithSnapshotter(c.RuntimeSnapshotter(ctx, ociRuntime)),
		// Prepare container rootfs. This is always writeable even if
		// the container wants a readonly rootfs since we want to give
		// the runtime (runc) a chance to modify (e.g. to create mount
		// points corresponding to spec.Mounts) before making the
		// rootfs readonly (requested by spec.Root.Readonly).
		customopts.WithNewSnapshot(id, containerdImage, sOpts...),
	}
	if len(volumeMounts) > 0 {
		mountMap := make(map[string]string)
		for _, v := range volumeMounts {
			mountMap[filepath.Clean(v.HostPath)] = v.ContainerPath
		}
		opts = append(opts, customopts.WithVolumes(mountMap, platform))
	}
	meta.ImageRef = image.ID
	meta.StopSignal = image.ImageSpec.Config.StopSignal

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
		return nil, fmt.Errorf("failed to create container io: %w", err)
	}
	defer func() {
		if retErr != nil {
			if err := containerIO.Close(); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to close container io %q", id)
			}
		}
	}()

	specOpts, err := c.platformSpecOpts(platform, config, &image.ImageSpec.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec opts: %w", err)
	}

	containerLabels := buildLabels(config.Labels, image.ImageSpec.Config.Labels, crilabels.ContainerKindContainer)

	// TODO the sandbox in the cache should hold this info
	runtimeName, runtimeOption, err := c.runtimeInfo(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("unable to get sandbox %q runtime info: %w", sandboxID, err)
	}

	opts = append(opts,
		containerd.WithSpec(spec, specOpts...),
		containerd.WithRuntime(runtimeName, runtimeOption),
		containerd.WithContainerLabels(containerLabels),
		containerd.WithContainerExtension(crilabels.ContainerMetadataExtension, &meta),
	)

	opts = append(opts, containerd.WithSandbox(sandboxID))

	opts = append(opts, c.nri.WithContainerAdjustment())
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := util.DeferContext()
			defer deferCancel()
			c.nri.UndoCreateContainer(deferCtx, &sandbox, id, spec)
		}
	}()

	var cntr containerd.Container
	if cntr, err = c.client.NewContainer(ctx, id, opts...); err != nil {
		return nil, fmt.Errorf("failed to create containerd container: %w", err)
	}
	defer func() {
		if retErr != nil {
			deferCtx, deferCancel := util.DeferContext()
			defer deferCancel()
			if err := cntr.Delete(deferCtx, containerd.WithSnapshotCleanup); err != nil {
				log.G(ctx).WithError(err).Errorf("Failed to delete containerd container %q", id)
			}
		}
	}()

	status := containerstore.Status{CreatedAt: time.Now().UnixNano()}
	status = copyResourcesToStatus(spec, status)
	container, err := containerstore.NewContainer(meta,
		containerstore.WithStatus(status, containerRootDir),
		containerstore.WithContainer(cntr),
		containerstore.WithContainerIO(containerIO),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create internal container object for %q: %w", id, err)
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
	if err := c.containerStore.Add(container); err != nil {
		return nil, fmt.Errorf("failed to add container %q into store: %w", id, err)
	}

	c.generateAndSendContainerEvent(ctx, id, sandboxID, runtime.ContainerEventType_CONTAINER_CREATED_EVENT)

	err = c.nri.PostCreateContainer(ctx, &sandbox, &container)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("NRI post-create notification failed")
	}

	containerCreateTimer.WithValues(ociRuntime.Type).UpdateSince(start)

	return &runtime.CreateContainerResponse{ContainerId: id}, nil
}

// volumeMounts sets up image volumes for container. Rely on the removal of container
// root directory to do cleanup. Note that image volume will be skipped, if there is criMounts
// specified with the same destination.
func (c *criService) volumeMounts(platform platforms.Platform, containerRootDir string, containerConfig *runtime.ContainerConfig, config *imagespec.ImageConfig) []*runtime.Mount {
	var uidMappings, gidMappings []*runtime.IDMapping
	if platform.OS == "linux" {
		if usernsOpts := containerConfig.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions(); usernsOpts != nil {
			uidMappings = usernsOpts.GetUids()
			gidMappings = usernsOpts.GetGids()
		}
	}

	criMounts := containerConfig.GetMounts()

	if len(config.Volumes) == 0 {
		return nil
	}
	var mounts []*runtime.Mount
	for dst := range config.Volumes {
		if isInCRIMounts(dst, criMounts) {
			// Skip the image volume, if there is CRI defined volume mapping.
			// TODO(random-liu): This should be handled by Kubelet in the future.
			// Kubelet should decide what to use for image volume, and also de-duplicate
			// the image volume and user mounts.
			continue
		}
		volumeID := util.GenerateID()
		src := filepath.Join(containerRootDir, "volumes", volumeID)
		// When the platform OS is Linux, ensure dst is a _Linux_ abs path.
		// We can't use filepath.IsAbs() because, when executing on Windows, it checks for
		// Windows abs paths.
		if platform.OS == "linux" && !strings.HasPrefix(dst, "/") {
			// On Windows, ToSlash() is needed to ensure the path is a valid Linux path.
			// On Linux, ToSlash() is a no-op.
			oldDst := dst
			dst = filepath.ToSlash(filepath.Join("/", dst))
			log.L.Debugf("Volume destination %q is not absolute, converted to %q", oldDst, dst)
		}
		// addOCIBindMounts will create these volumes.
		mounts = append(mounts, &runtime.Mount{
			ContainerPath:  dst,
			HostPath:       src,
			SelinuxRelabel: true,
			UidMappings:    uidMappings,
			GidMappings:    gidMappings,
		})
	}
	return mounts
}

// runtimeSpec returns a default runtime spec used in cri-containerd.
func (c *criService) runtimeSpec(id string, platform platforms.Platform, baseSpecFile string, opts ...oci.SpecOpts) (*runtimespec.Spec, error) {
	// GenerateSpec needs namespace.
	ctx := util.NamespacedContext()
	container := &containers.Container{ID: id}

	if baseSpecFile != "" {
		baseSpec, ok := c.baseOCISpecs[baseSpecFile]
		if !ok {
			return nil, fmt.Errorf("can't find base OCI spec %q", baseSpecFile)
		}

		spec := oci.Spec{}
		if err := util.DeepCopy(&spec, &baseSpec); err != nil {
			return nil, fmt.Errorf("failed to clone OCI spec: %w", err)
		}

		// Fix up cgroups path
		applyOpts := append([]oci.SpecOpts{oci.WithNamespacedCgroup()}, opts...)

		if err := oci.ApplyOpts(ctx, nil, container, &spec, applyOpts...); err != nil {
			return nil, fmt.Errorf("failed to apply OCI options: %w", err)
		}

		return &spec, nil
	}

	spec, err := oci.GenerateSpecWithPlatform(ctx, nil, platforms.Format(platform), container, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spec: %w", err)
	}

	return spec, nil
}

const (
	// relativeRootfsPath is the rootfs path relative to bundle path.
	relativeRootfsPath = "rootfs"
	// hostnameEnv is the key for HOSTNAME env.
	hostnameEnv = "HOSTNAME"
)

// generateUserString generates valid user string based on OCI Image Spec
// v1.0.0.
//
// CRI defines that the following combinations are valid:
//
// (none) -> ""
// username -> username
// username, uid -> username
// username, uid, gid -> username:gid
// username, gid -> username:gid
// uid -> uid
// uid, gid -> uid:gid
// gid -> error
//
// TODO(random-liu): Add group name support in CRI.
func generateUserString(username string, uid, gid *runtime.Int64Value) (string, error) {
	var userstr, groupstr string
	if uid != nil {
		userstr = strconv.FormatInt(uid.GetValue(), 10)
	}
	if username != "" {
		userstr = username
	}
	if gid != nil {
		groupstr = strconv.FormatInt(gid.GetValue(), 10)
	}
	if userstr == "" {
		if groupstr != "" {
			return "", fmt.Errorf("user group %q is specified without user", groupstr)
		}
		return "", nil
	}
	if groupstr != "" {
		userstr = userstr + ":" + groupstr
	}
	return userstr, nil
}

// platformSpecOpts adds additional runtime spec options that may rely on
// runtime information (rootfs mounted), or platform specific checks with
// no defined workaround (yet) to specify for other platforms.
func (c *criService) platformSpecOpts(
	platform platforms.Platform,
	config *runtime.ContainerConfig,
	imageConfig *imagespec.ImageConfig,
) ([]oci.SpecOpts, error) {
	var specOpts []oci.SpecOpts

	// First deal with the set of options we can use across platforms currently.
	// Linux user strings have workarounds on other platforms to avoid needing to
	// mount the rootfs, but on Linux hosts it must be mounted
	//
	// TODO(dcantah): I think the seccomp package can be made to compile on
	// !linux and used here as well.
	if platform.OS == "linux" {
		// Set container username. This could only be done by containerd, because it needs
		// access to the container rootfs. Pass user name to containerd, and let it overwrite
		// the spec for us.
		securityContext := config.GetLinux().GetSecurityContext()
		userstr, err := generateUserString(
			securityContext.GetRunAsUsername(),
			securityContext.GetRunAsUser(),
			securityContext.GetRunAsGroup())
		if err != nil {
			return nil, fmt.Errorf("failed to generate user string: %w", err)
		}
		if userstr == "" {
			// Lastly, since no user override was passed via CRI try to set via OCI
			// Image
			userstr = imageConfig.User
		}
		if userstr != "" {
			specOpts = append(specOpts, oci.WithUser(userstr))
		}
	}

	// Now grab the truly platform specific options (seccomp, apparmor etc. for linux
	// for example).
	ctrSpecOpts, err := c.containerSpecOpts(config, imageConfig)
	if err != nil {
		return nil, err
	}
	specOpts = append(specOpts, ctrSpecOpts...)

	return specOpts, nil
}

// buildContainerSpec build container's OCI spec depending on controller's target platform OS.
func (c *criService) buildContainerSpec(
	platform platforms.Platform,
	id string,
	sandboxID string,
	sandboxPid uint32,
	netNSPath string,
	containerName string,
	imageName string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig,
	extraMounts []*runtime.Mount,
	ociRuntime criconfig.Runtime,
) (_ *runtimespec.Spec, retErr error) {
	var (
		specOpts []oci.SpecOpts
		err      error

		// Platform helpers
		isLinux   = platform.OS == "linux"
		isWindows = platform.OS == "windows"
		isDarwin  = platform.OS == "darwin"
	)

	switch {
	case isLinux:
		// Generate container mounts.
		// No mounts are passed for other platforms.
		linuxMounts := c.linuxContainerMounts(sandboxID, config)

		specOpts, err = c.buildLinuxSpec(
			id,
			sandboxID,
			sandboxPid,
			netNSPath,
			containerName,
			imageName,
			config,
			sandboxConfig,
			imageConfig,
			append(linuxMounts, extraMounts...),
			ociRuntime,
		)
	case isWindows:
		specOpts, err = c.buildWindowsSpec(
			id,
			sandboxID,
			sandboxPid,
			netNSPath,
			containerName,
			imageName,
			config,
			sandboxConfig,
			imageConfig,
			extraMounts,
			ociRuntime,
		)
	case isDarwin:
		specOpts, err = c.buildDarwinSpec(
			id,
			sandboxID,
			containerName,
			imageName,
			config,
			sandboxConfig,
			imageConfig,
			extraMounts,
			ociRuntime,
		)
	default:
		return nil, fmt.Errorf("unsupported spec platform: %s", platform.OS)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate spec opts: %w", err)
	}

	return c.runtimeSpec(id, platform, ociRuntime.BaseRuntimeSpec, specOpts...)
}

func (c *criService) buildLinuxSpec(
	id string,
	sandboxID string,
	sandboxPid uint32,
	netNSPath string,
	containerName string,
	imageName string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig,
	extraMounts []*runtime.Mount,
	ociRuntime criconfig.Runtime,
) (_ []oci.SpecOpts, retErr error) {
	specOpts := []oci.SpecOpts{
		oci.WithoutRunMount,
	}
	// only clear the default security settings if the runtime does not have a custom
	// base runtime spec spec.  Admins can use this functionality to define
	// default ulimits, seccomp, or other default settings.
	if ociRuntime.BaseRuntimeSpec == "" {
		specOpts = append(specOpts, customopts.WithoutDefaultSecuritySettings)
	}

	specOpts = append(specOpts,
		customopts.WithRelativeRoot(relativeRootfsPath),
		customopts.WithProcessArgs(config, imageConfig),
		oci.WithDefaultPathEnv,
		// this will be set based on the security context below
		oci.WithNewPrivileges,
	)

	if config.GetWorkingDir() != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(config.GetWorkingDir()))
	} else if imageConfig.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(imageConfig.WorkingDir))
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
		if hostname, err = c.os.Hostname(); err != nil {
			return nil, err
		}
	}
	specOpts = append(specOpts, oci.WithEnv([]string{hostnameEnv + "=" + hostname}))

	// Apply envs from image config first, so that envs from container config
	// can override them.
	env := append([]string{}, imageConfig.Env...)
	for _, e := range config.GetEnvs() {
		env = append(env, e.GetKey()+"="+e.GetValue())
	}
	specOpts = append(specOpts, oci.WithEnv(env))

	securityContext := config.GetLinux().GetSecurityContext()
	labelOptions, err := toLabel(securityContext.GetSelinuxOptions())
	if err != nil {
		return nil, err
	}
	if len(labelOptions) == 0 {
		// Use pod level SELinux config
		if sandbox, err := c.sandboxStore.Get(sandboxID); err == nil {
			labelOptions, err = selinux.DupSecOpt(sandbox.ProcessLabel)
			if err != nil {
				return nil, err
			}
		}
	}

	processLabel, mountLabel, err := label.InitLabels(labelOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to init selinux options %+v: %w", securityContext.GetSelinuxOptions(), err)
	}
	defer func() {
		if retErr != nil {
			selinux.ReleaseLabel(processLabel)
		}
	}()

	specOpts = append(specOpts, customopts.WithMounts(c.os, config, extraMounts, mountLabel))

	if !c.config.DisableProcMount {
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

	specOpts = append(specOpts, customopts.WithDevices(c.os, config, c.config.DeviceOwnershipFromSecurityContext),
		customopts.WithCapabilities(securityContext, c.allCaps))

	if securityContext.GetPrivileged() {
		if !sandboxConfig.GetLinux().GetSecurityContext().GetPrivileged() {
			return nil, errors.New("no privileged container allowed in sandbox")
		}
		specOpts = append(specOpts, oci.WithPrivileged)
		if !ociRuntime.PrivilegedWithoutHostDevices {
			specOpts = append(specOpts, oci.WithHostDevices, oci.WithAllDevicesAllowed)
		} else if ociRuntime.PrivilegedWithoutHostDevicesAllDevicesAllowed {
			// allow rwm on all devices for the container
			specOpts = append(specOpts, oci.WithAllDevicesAllowed)
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

	if c.config.DisableCgroup {
		specOpts = append(specOpts, customopts.WithDisabledCgroups)
	} else {
		specOpts = append(specOpts, customopts.WithResources(config.GetLinux().GetResources(), c.config.TolerateMissingHugetlbController, c.config.DisableHugetlbController))
		if sandboxConfig.GetLinux().GetCgroupParent() != "" {
			cgroupsPath := getCgroupsPath(sandboxConfig.GetLinux().GetCgroupParent(), id)
			specOpts = append(specOpts, oci.WithCgroup(cgroupsPath))
		}
	}

	supplementalGroups := securityContext.GetSupplementalGroups()

	// Get blockio class
	blockIOClass, err := c.blockIOClassFromAnnotations(config.GetMetadata().GetName(), config.Annotations, sandboxConfig.Annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to set blockio class: %w", err)
	}
	if blockIOClass != "" {
		if linuxBlockIO, err := blockio.ClassNameToLinuxOCI(blockIOClass); err == nil {
			specOpts = append(specOpts, oci.WithBlockIO(linuxBlockIO))
		} else {
			return nil, err
		}
	}

	// Get RDT class
	rdtClass, err := c.rdtClassFromAnnotations(config.GetMetadata().GetName(), config.Annotations, sandboxConfig.Annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to set RDT class: %w", err)
	}
	if rdtClass != "" {
		specOpts = append(specOpts, oci.WithRdt(rdtClass, "", ""))
	}

	for pKey, pValue := range getPassthroughAnnotations(sandboxConfig.Annotations,
		ociRuntime.PodAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	for pKey, pValue := range getPassthroughAnnotations(config.Annotations,
		ociRuntime.ContainerAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	// Default target PID namespace is the sandbox PID.
	targetPid := sandboxPid
	// If the container targets another container's PID namespace,
	// set targetPid to the PID of that container.
	nsOpts := securityContext.GetNamespaceOptions()
	if nsOpts.GetPid() == runtime.NamespaceMode_TARGET {
		targetContainer, err := c.validateTargetContainer(sandboxID, nsOpts.TargetId)
		if err != nil {
			return nil, fmt.Errorf("invalid target container: %w", err)
		}

		status := targetContainer.Status.Get()
		targetPid = status.Pid
	}

	uids, gids, err := parseUsernsIDs(nsOpts.GetUsernsOptions())
	if err != nil {
		return nil, fmt.Errorf("user namespace configuration: %w", err)
	}

	// Check sandbox userns config is consistent with container config.
	sandboxUsernsOpts := sandboxConfig.GetLinux().GetSecurityContext().GetNamespaceOptions().GetUsernsOptions()
	if !sameUsernsConfig(sandboxUsernsOpts, nsOpts.GetUsernsOptions()) {
		return nil, fmt.Errorf("user namespace config for sandbox is different from container. Sandbox userns config: %v - Container userns config: %v", sandboxUsernsOpts, nsOpts.GetUsernsOptions())
	}

	specOpts = append(specOpts,
		customopts.WithOOMScoreAdj(config, c.config.RestrictOOMScoreAdj),
		customopts.WithPodNamespaces(securityContext, sandboxPid, targetPid, uids, gids),
		customopts.WithSupplementalGroups(supplementalGroups),
	)
	specOpts = append(
		specOpts,
		annotations.DefaultCRIAnnotations(sandboxID, containerName, imageName, sandboxConfig, false)...,
	)

	// cgroupns is used for hiding /sys/fs/cgroup from containers.
	// For compatibility, cgroupns is not used when running in cgroup v1 mode or in privileged.
	// https://github.com/containers/libpod/issues/4363
	// https://github.com/kubernetes/enhancements/blob/0e409b47497e398b369c281074485c8de129694f/keps/sig-node/20191118-cgroups-v2.md#cgroup-namespace
	if isUnifiedCgroupsMode() && !securityContext.GetPrivileged() {
		specOpts = append(specOpts, oci.WithLinuxNamespace(runtimespec.LinuxNamespace{Type: runtimespec.CgroupNamespace}))
	}

	return specOpts, nil
}

func (c *criService) buildWindowsSpec(
	id string,
	sandboxID string,
	sandboxPid uint32,
	netNSPath string,
	containerName string,
	imageName string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig,
	extraMounts []*runtime.Mount,
	ociRuntime criconfig.Runtime,
) (_ []oci.SpecOpts, retErr error) {
	var specOpts []oci.SpecOpts
	specOpts = append(specOpts, customopts.WithProcessCommandLineOrArgsForWindows(config, imageConfig))

	// All containers in a pod need to have HostProcess set if it was set on the pod,
	// and vice versa no containers in the pod can be HostProcess if the pods spec
	// didn't have the field set. The only case that is valid is if these are the same value.
	cntrHpc := config.GetWindows().GetSecurityContext().GetHostProcess()
	sandboxHpc := sandboxConfig.GetWindows().GetSecurityContext().GetHostProcess()
	if cntrHpc != sandboxHpc {
		return nil, errors.New("pod spec and all containers inside must have the HostProcess field set to be valid")
	}

	if config.GetWorkingDir() != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(config.GetWorkingDir()))
	} else if imageConfig.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(imageConfig.WorkingDir))
	}

	if config.GetTty() {
		specOpts = append(specOpts, oci.WithTTY)
	}

	// Apply envs from image config first, so that envs from container config
	// can override them.
	env := append([]string{}, imageConfig.Env...)
	for _, e := range config.GetEnvs() {
		env = append(env, e.GetKey()+"="+e.GetValue())
	}
	specOpts = append(specOpts, oci.WithEnv(env))

	specOpts = append(specOpts,
		// Clear the root location since hcsshim expects it.
		// NOTE: readonly rootfs doesn't work on windows.
		customopts.WithoutRoot,
		oci.WithWindowsNetworkNamespace(netNSPath),
		oci.WithHostname(sandboxConfig.GetHostname()),
	)

	specOpts = append(specOpts, customopts.WithWindowsMounts(c.os, config, extraMounts), customopts.WithWindowsDevices(config))

	// Start with the image config user and override below if RunAsUsername is not "".
	username := imageConfig.User

	windowsConfig := config.GetWindows()
	if windowsConfig != nil {
		specOpts = append(specOpts, customopts.WithWindowsResources(windowsConfig.GetResources()))
		securityCtx := windowsConfig.GetSecurityContext()
		if securityCtx != nil {
			runAsUser := securityCtx.GetRunAsUsername()
			if runAsUser != "" {
				username = runAsUser
			}
			cs := securityCtx.GetCredentialSpec()
			if cs != "" {
				specOpts = append(specOpts, customopts.WithWindowsCredentialSpec(cs))
			}
		}
	}

	// There really isn't a good Windows way to verify that the username is available in the
	// image as early as here like there is for Linux. Later on in the stack hcsshim
	// will handle the behavior of erroring out if the user isn't available in the image
	// when trying to run the init process.
	specOpts = append(specOpts, oci.WithUser(username))

	for pKey, pValue := range getPassthroughAnnotations(sandboxConfig.Annotations,
		ociRuntime.PodAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	for pKey, pValue := range getPassthroughAnnotations(config.Annotations,
		ociRuntime.ContainerAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	specOpts = append(specOpts, customopts.WithAnnotation(annotations.WindowsHostProcess, strconv.FormatBool(sandboxHpc)))
	specOpts = append(specOpts,
		annotations.DefaultCRIAnnotations(sandboxID, containerName, imageName, sandboxConfig, false)...,
	)

	return specOpts, nil
}

func (c *criService) buildDarwinSpec(
	id string,
	sandboxID string,
	containerName string,
	imageName string,
	config *runtime.ContainerConfig,
	sandboxConfig *runtime.PodSandboxConfig,
	imageConfig *imagespec.ImageConfig,
	extraMounts []*runtime.Mount,
	ociRuntime criconfig.Runtime,
) (_ []oci.SpecOpts, retErr error) {
	specOpts := []oci.SpecOpts{
		customopts.WithProcessArgs(config, imageConfig),
	}

	if config.GetWorkingDir() != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(config.GetWorkingDir()))
	} else if imageConfig.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(imageConfig.WorkingDir))
	}

	if config.GetTty() {
		specOpts = append(specOpts, oci.WithTTY)
	}

	// Apply envs from image config first, so that envs from container config
	// can override them.
	env := append([]string{}, imageConfig.Env...)
	for _, e := range config.GetEnvs() {
		env = append(env, e.GetKey()+"="+e.GetValue())
	}
	specOpts = append(specOpts, oci.WithEnv(env))

	specOpts = append(specOpts, customopts.WithDarwinMounts(c.os, config, extraMounts))

	for pKey, pValue := range getPassthroughAnnotations(sandboxConfig.Annotations,
		ociRuntime.PodAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	for pKey, pValue := range getPassthroughAnnotations(config.Annotations,
		ociRuntime.ContainerAnnotations) {
		specOpts = append(specOpts, customopts.WithAnnotation(pKey, pValue))
	}

	specOpts = append(specOpts,
		annotations.DefaultCRIAnnotations(sandboxID, containerName, imageName, sandboxConfig, false)...,
	)

	return specOpts, nil
}

// linuxContainerMounts sets up necessary container system file mounts
// including /dev/shm, /etc/hosts and /etc/resolv.conf.
func (c *criService) linuxContainerMounts(sandboxID string, config *runtime.ContainerConfig) []*runtime.Mount {
	var mounts []*runtime.Mount
	securityContext := config.GetLinux().GetSecurityContext()
	var uidMappings, gidMappings []*runtime.IDMapping
	if usernsOpts := securityContext.GetNamespaceOptions().GetUsernsOptions(); usernsOpts != nil {
		uidMappings = usernsOpts.GetUids()
		gidMappings = usernsOpts.GetGids()
	}

	if !isInCRIMounts(etcHostname, config.GetMounts()) {
		// /etc/hostname is added since 1.1.6, 1.2.4 and 1.3.
		// For in-place upgrade, the old sandbox doesn't have the hostname file,
		// do not mount this in that case.
		// TODO(random-liu): Remove the check and always mount this when
		// containerd 1.1 and 1.2 are deprecated.
		hostpath := c.getSandboxHostname(sandboxID)
		if _, err := c.os.Stat(hostpath); err == nil {
			mounts = append(mounts, &runtime.Mount{
				ContainerPath:  etcHostname,
				HostPath:       hostpath,
				Readonly:       securityContext.GetReadonlyRootfs(),
				SelinuxRelabel: true,
				UidMappings:    uidMappings,
				GidMappings:    gidMappings,
			})
		}
	}

	if !isInCRIMounts(etcHosts, config.GetMounts()) {
		mounts = append(mounts, &runtime.Mount{
			ContainerPath:  etcHosts,
			HostPath:       c.getSandboxHosts(sandboxID),
			Readonly:       securityContext.GetReadonlyRootfs(),
			SelinuxRelabel: true,
			UidMappings:    uidMappings,
			GidMappings:    gidMappings,
		})
	}

	// Mount sandbox resolv.config.
	// TODO: Need to figure out whether we should always mount it as read-only
	if !isInCRIMounts(resolvConfPath, config.GetMounts()) {
		mounts = append(mounts, &runtime.Mount{
			ContainerPath:  resolvConfPath,
			HostPath:       c.getResolvPath(sandboxID),
			Readonly:       securityContext.GetReadonlyRootfs(),
			SelinuxRelabel: true,
			UidMappings:    uidMappings,
			GidMappings:    gidMappings,
		})
	}

	if !isInCRIMounts(devShm, config.GetMounts()) {
		sandboxDevShm := c.getSandboxDevShm(sandboxID)
		if securityContext.GetNamespaceOptions().GetIpc() == runtime.NamespaceMode_NODE {
			sandboxDevShm = devShm
		}
		mounts = append(mounts, &runtime.Mount{
			ContainerPath:  devShm,
			HostPath:       sandboxDevShm,
			Readonly:       false,
			SelinuxRelabel: sandboxDevShm != devShm,
			// XXX: tmpfs support for idmap mounts got merged in
			// Linux 6.3.
			// Our Ubuntu 22.04 CI runs with 5.15 kernels, so
			// disabling idmap mounts for this case makes the CI
			// happy (the other fs used support idmap mounts in 5.15
			// kernels).
			// We can enable this at a later stage, but as this
			// tmpfs mount is exposed empty to the container (no
			// prepopulated files) and using the hostIPC with userns
			// is blocked by k8s, we can just avoid using the
			// mappings and it should work fine.
		})
	}
	return mounts
}

func (c *criService) runtimeInfo(ctx context.Context, id string) (string, typeurl.Any, error) {
	sandboxInfo, err := c.client.SandboxStore().Get(ctx, id)
	if err == nil {
		return sandboxInfo.Runtime.Name, sandboxInfo.Runtime.Options, nil
	}
	sandboxContainer, legacyErr := c.client.ContainerService().Get(ctx, id)
	if legacyErr == nil {
		return sandboxContainer.Runtime.Name, sandboxContainer.Runtime.Options, nil
	}

	return "", nil, err
}
