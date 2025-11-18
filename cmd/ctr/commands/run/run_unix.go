//go:build !windows

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

package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/log"
	"github.com/containerd/platforms"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/contrib/apparmor"
	"github.com/containerd/containerd/v2/contrib/nvidia"
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/snapshots"
	cdispec "github.com/containerd/containerd/v2/pkg/cdi"
	"github.com/containerd/containerd/v2/pkg/oci"

	"github.com/intel/goresctrl/pkg/blockio"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli/v2"
	"tags.cncf.io/container-device-interface/pkg/cdi"
	"tags.cncf.io/container-device-interface/pkg/parser"
)

var platformRunFlags = []cli.Flag{
	&cli.StringSliceFlag{
		Name:  "uidmap",
		Usage: "Run inside a user namespace with the specified UID mapping ranges; specified with the format `container-uid:host-uid:length`",
	},
	&cli.StringSliceFlag{
		Name:  "gidmap",
		Usage: "Run inside a user namespace with the specified GID mapping ranges; specified with the format `container-gid:host-gid:length`",
	},
	&cli.BoolFlag{
		Name:  "remap-labels",
		Usage: "Provide the user namespace ID remapping to the snapshotter via label options; requires snapshotter support",
	},
	&cli.BoolFlag{
		Name:  "privileged-without-host-devices",
		Usage: "Don't pass all host devices to privileged container",
	},
	&cli.Float64Flag{
		Name:  "cpus",
		Usage: "Set the CFS cpu quota",
		Value: 0.0,
	},
	&cli.IntFlag{
		Name:  "cpu-shares",
		Usage: "Set the cpu shares",
		Value: 1024,
	},
	&cli.StringFlag{
		Name:  "cpuset-cpus",
		Usage: "Set the CPUs the container will run in (e.g., 1-2,4)",
	},
	&cli.StringFlag{
		Name:  "cpuset-mems",
		Usage: "Set the memory nodes the container will run in (e.g., 1-2,4)",
	},
	&cli.StringFlag{
		Name:  "rlimit-nofile",
		Usage: "Set RLIMIT_NOFILE (soft:hard)",
	},
}

// NewContainer creates a new container
func NewContainer(ctx context.Context, client *containerd.Client, cliContext *cli.Context) (containerd.Container, error) {
	var (
		id     string
		config = cliContext.IsSet("config")
	)
	if config {
		id = cliContext.Args().First()
	} else {
		id = cliContext.Args().Get(1)
	}

	platform := cliContext.String("platform")
	if platform == "" {
		plat := platforms.DefaultSpec()
		switch plat.OS {
		case "linux":
		case "freebsd":
			// TODO: freebsd support is under development, allow platform to remain unchanged.
			// A freebsd spec generator must be implemented to make use of it, until then,
			// either a spec must be provided or the runtime can convert from the linux spec.
		default:
			// Other OSes do not have a supported container runtime, to use experimental runtimes,
			// specs must be explicitly provided. Once there is a support spec generator, then
			// the default platform can be added above to not default to linux.
			plat.OS = "linux"
		}
		platform = platforms.FormatAll(plat)
	}

	var (
		opts  []oci.SpecOpts
		cOpts []containerd.NewContainerOpts
		spec  containerd.NewContainerOpts
	)

	if sandbox := cliContext.String("sandbox"); sandbox != "" {
		cOpts = append(cOpts, containerd.WithSandbox(sandbox))
	}

	if config {
		cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(cliContext.StringSlice("label"))))
		opts = append(opts, oci.WithSpecFromFile(cliContext.String("config")))
	} else {
		var (
			ref = cliContext.Args().First()
			// for container's id is Args[1]
			args = cliContext.Args().Slice()[2:]
		)
		opts = append(opts, oci.WithDefaultSpecForPlatform(platform), oci.WithDefaultUnixDevices)
		if ef := cliContext.String("env-file"); ef != "" {
			opts = append(opts, oci.WithEnvFile(ef))
		}
		opts = append(opts, oci.WithEnv(cliContext.StringSlice("env")))
		opts = append(opts, withMounts(cliContext))

		if cliContext.Bool("rootfs") {
			rootfs, err := filepath.Abs(ref)
			if err != nil {
				return nil, err
			}
			opts = append(opts, oci.WithRootFSPath(rootfs))
			cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(cliContext.StringSlice("label"))))
		} else {
			snapshotter := cliContext.String("snapshotter")
			var image containerd.Image
			i, err := client.ImageService().Get(ctx, ref)
			if err != nil {
				return nil, err
			}
			if ps := cliContext.String("platform"); ps != "" {
				platform, err := platforms.Parse(ps)
				if err != nil {
					return nil, err
				}
				image = containerd.NewImageWithPlatform(client, i, platforms.Only(platform))
			} else {
				image = containerd.NewImage(client, i)
			}

			unpacked, err := image.IsUnpacked(ctx, snapshotter)
			if err != nil {
				return nil, err
			}
			if !unpacked {
				if err := image.Unpack(ctx, snapshotter, containerd.WithUnpackApplyOpts(diff.WithSyncFs(cliContext.Bool("sync-fs")))); err != nil {
					return nil, err
				}
			}
			labels := buildLabels(commands.LabelArgs(cliContext.StringSlice("label")), image.Labels())
			opts = append(opts, oci.WithImageConfig(image))
			cOpts = append(cOpts,
				containerd.WithImage(image),
				containerd.WithImageConfigLabels(image),
				containerd.WithAdditionalContainerLabels(labels),
				containerd.WithSnapshotter(snapshotter))

			if uidmaps, gidmaps := cliContext.StringSlice("uidmap"), cliContext.StringSlice("gidmap"); len(uidmaps) > 0 && len(gidmaps) > 0 {
				var uidSpec, gidSpec []specs.LinuxIDMapping
				if uidSpec, err = parseIDMappingOption(uidmaps); err != nil {
					return nil, err
				}
				if gidSpec, err = parseIDMappingOption(gidmaps); err != nil {
					return nil, err
				}
				opts = append(opts, oci.WithUserNamespace(uidSpec, gidSpec))
				// use snapshotter opts or the remapped snapshot support to shift the filesystem
				// currently the snapshotters known to support the labels are:
				// fuse-overlayfs - https://github.com/containerd/fuse-overlayfs-snapshotter
				// overlay - in case of idmapped mount points are supported by host kernel (Linux kernel 5.19)
				if cliContext.Bool("remap-labels") {
					cOpts = append(cOpts, containerd.WithNewSnapshot(id, image, containerd.WithUserNSRemapperLabels(uidSpec, gidSpec)))
				} else {
					cOpts = append(cOpts, containerd.WithUserNSRemappedSnapshot(id, image, uidSpec, gidSpec))
				}
			} else {
				// Even when "read-only" is set, we don't use KindView snapshot here. (#1495)
				// We pass writable snapshot to the OCI runtime, and the runtime remounts it as read-only,
				// after creating some mount points on demand.
				// For some snapshotter, such as overlaybd, it can provide 2 kind of writable snapshot(overlayfs dir or block-device)
				// by command label values.
				cOpts = append(cOpts, containerd.WithNewSnapshot(id, image,
					snapshots.WithLabels(commands.LabelArgs(cliContext.StringSlice("snapshotter-label")))))
			}
			cOpts = append(cOpts, containerd.WithImageStopSignal(image, "SIGTERM"))
		}
		if cliContext.Bool("read-only") {
			opts = append(opts, oci.WithRootFSReadonly())
		}
		if len(args) > 0 {
			opts = append(opts, oci.WithProcessArgs(args...))
		}
		if cwd := cliContext.String("cwd"); cwd != "" {
			opts = append(opts, oci.WithProcessCwd(cwd))
		}
		if user := cliContext.String("user"); user != "" {
			opts = append(opts, oci.WithUser(user), oci.WithAdditionalGIDs(user))
		}
		if cliContext.Bool("tty") {
			opts = append(opts, oci.WithTTY)
		}

		privileged := cliContext.Bool("privileged")
		privilegedWithoutHostDevices := cliContext.Bool("privileged-without-host-devices")
		if privilegedWithoutHostDevices && !privileged {
			return nil, errors.New("can't use 'privileged-without-host-devices' without 'privileged' specified")
		}
		if privileged {
			if privilegedWithoutHostDevices {
				opts = append(opts, oci.WithPrivileged)
			} else {
				opts = append(opts, oci.WithPrivileged, oci.WithAllDevicesAllowed, oci.WithHostDevices)
			}
		}

		if cliContext.Bool("net-host") {
			hostname, err := os.Hostname()
			if err != nil {
				return nil, fmt.Errorf("get hostname: %w", err)
			}
			opts = append(opts,
				oci.WithHostNamespace(specs.NetworkNamespace),
				oci.WithHostHostsFile,
				oci.WithHostResolvconf,
				oci.WithEnv([]string{fmt.Sprintf("HOSTNAME=%s", hostname)}),
			)
		}
		if annoStrings := cliContext.StringSlice("annotation"); len(annoStrings) > 0 {
			annos, err := commands.AnnotationArgs(annoStrings)
			if err != nil {
				return nil, err
			}
			opts = append(opts, oci.WithAnnotations(annos))
		}

		if caps := cliContext.StringSlice("cap-add"); len(caps) > 0 {
			for _, c := range caps {
				if !strings.HasPrefix(c, "CAP_") {
					return nil, errors.New("capabilities must be specified with 'CAP_' prefix")
				}
			}
			opts = append(opts, oci.WithAddedCapabilities(caps))
		}

		if caps := cliContext.StringSlice("cap-drop"); len(caps) > 0 {
			for _, c := range caps {
				if !strings.HasPrefix(c, "CAP_") {
					return nil, errors.New("capabilities must be specified with 'CAP_' prefix")
				}
			}
			opts = append(opts, oci.WithDroppedCapabilities(caps))
		}

		seccompProfile := cliContext.String("seccomp-profile")

		if !cliContext.Bool("seccomp") && seccompProfile != "" {
			return nil, errors.New("seccomp must be set to true, if using a custom seccomp-profile")
		}

		if cliContext.Bool("seccomp") {
			if seccompProfile != "" {
				opts = append(opts, seccomp.WithProfile(seccompProfile))
			} else {
				opts = append(opts, seccomp.WithDefaultProfile())
			}
		}

		if s := cliContext.String("apparmor-default-profile"); len(s) > 0 {
			opts = append(opts, apparmor.WithDefaultProfile(s))
		}

		if s := cliContext.String("apparmor-profile"); len(s) > 0 {
			if len(cliContext.String("apparmor-default-profile")) > 0 {
				return nil, errors.New("apparmor-profile conflicts with apparmor-default-profile")
			}
			opts = append(opts, apparmor.WithProfile(s))
		}

		if cpus := cliContext.Float64("cpus"); cpus > 0.0 {
			var (
				period = uint64(100000)
				quota  = int64(cpus * 100000.0)
			)
			opts = append(opts, oci.WithCPUCFS(quota, period))
		}

		if cpusetCpus := cliContext.String("cpuset-cpus"); len(cpusetCpus) > 0 {
			opts = append(opts, oci.WithCPUs(cpusetCpus))
		}

		if cpusetMems := cliContext.String("cpuset-mems"); len(cpusetMems) > 0 {
			opts = append(opts, oci.WithCPUsMems(cpusetMems))
		}

		if shares := cliContext.Int("cpu-shares"); shares > 0 {
			opts = append(opts, oci.WithCPUShares(uint64(shares)))
		}

		quota := cliContext.Int64("cpu-quota")
		period := cliContext.Uint64("cpu-period")
		if quota != -1 || period != 0 {
			if cpus := cliContext.Float64("cpus"); cpus > 0.0 {
				return nil, errors.New("cpus and quota/period should be used separately")
			}
			opts = append(opts, oci.WithCPUCFS(quota, period))
		}

		if burst := cliContext.Uint64("cpu-burst"); burst != 0 {
			opts = append(opts, oci.WithCPUBurst(burst))
		}

		joinNs := cliContext.StringSlice("with-ns")
		for _, ns := range joinNs {
			nsType, nsPath, ok := strings.Cut(ns, ":")
			if !ok {
				return nil, errors.New("joining a Linux namespace using --with-ns requires the format 'nstype:path'")
			}
			if !validNamespace(nsType) {
				return nil, errors.New("the Linux namespace type specified in --with-ns is not valid: " + nsType)
			}
			opts = append(opts, oci.WithLinuxNamespace(specs.LinuxNamespace{
				Type: specs.LinuxNamespaceType(nsType),
				Path: nsPath,
			}))
		}
		if cliContext.IsSet("gpus") {
			opts = append(opts, nvidia.WithGPUs(nvidia.WithDevices(cliContext.IntSlice("gpus")...), nvidia.WithAllCapabilities))
		}
		if cliContext.IsSet("allow-new-privs") {
			opts = append(opts, oci.WithNewPrivileges)
		}
		if cliContext.IsSet("cgroup") {
			// NOTE: can be set to "" explicitly for disabling cgroup.
			opts = append(opts, oci.WithCgroup(cliContext.String("cgroup")))
		}
		limit := cliContext.Uint64("memory-limit")
		if limit != 0 {
			opts = append(opts, oci.WithMemoryLimit(limit))
		}
		var cdiDeviceIDs []string
		for _, dev := range cliContext.StringSlice("device") {
			if parser.IsQualifiedName(dev) {
				cdiDeviceIDs = append(cdiDeviceIDs, dev)
				continue
			}
			opts = append(opts, oci.WithDevices(dev, "", "rwm"))
		}
		if len(cdiDeviceIDs) > 0 {
			opts = append(opts, withStaticCDIRegistry())
		}
		opts = append(opts, cdispec.WithCDIDevices(cdiDeviceIDs...))

		rootfsPropagation := cliContext.String("rootfs-propagation")
		if rootfsPropagation != "" {
			opts = append(opts, func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
				if s.Linux != nil {
					s.Linux.RootfsPropagation = rootfsPropagation
				} else {
					s.Linux = &specs.Linux{
						RootfsPropagation: rootfsPropagation,
					}
				}

				return nil
			})
		}

		if c := cliContext.String("blockio-config-file"); c != "" {
			if err := blockio.SetConfigFromFile(c, false); err != nil {
				return nil, fmt.Errorf("blockio-config-file error: %w", err)
			}
		}

		if c := cliContext.String("blockio-class"); c != "" {
			if linuxBlockIO, err := blockio.OciLinuxBlockIO(c); err == nil {
				opts = append(opts, oci.WithBlockIO(linuxBlockIO))
			} else {
				return nil, fmt.Errorf("blockio-class error: %w", err)
			}
		}
		if c := cliContext.String("rdt-class"); c != "" {
			opts = append(opts, oci.WithRdt(c, "", ""))
		}
		if hostname := cliContext.String("hostname"); hostname != "" {
			opts = append(opts, oci.WithHostname(hostname))
		}
		if c := cliContext.String("rlimit-nofile"); c != "" {
			softS, hardS, found := strings.Cut(c, ":")
			if !found {
				hardS = softS
			}
			soft, err := strconv.ParseUint(softS, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse rlimit-nofile %q: %w", c, err)
			}
			hard, err := strconv.ParseUint(hardS, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse rlimit-nofile %q: %w", c, err)
			}
			rlimit := &specs.POSIXRlimit{
				Type: "RLIMIT_NOFILE",
				Hard: hard,
				Soft: soft,
			}
			opts = append(opts, oci.WithRlimit(rlimit))
		}
	}

	if cliContext.Bool("cni") {
		cniMeta := &commands.NetworkMetaData{EnableCni: true}
		cOpts = append(cOpts, containerd.WithContainerExtension(commands.CtrCniMetadataExtension, cniMeta))
	}

	runtimeOpts, err := commands.RuntimeOptions(cliContext)
	if err != nil {
		return nil, err
	}
	cOpts = append(cOpts, containerd.WithRuntime(cliContext.String("runtime"), runtimeOpts))

	opts = append(opts, oci.WithAnnotations(commands.LabelArgs(cliContext.StringSlice("label"))))
	var s specs.Spec
	spec = containerd.WithSpec(&s, opts...)

	cOpts = append(cOpts, spec)

	// oci.WithImageConfig (WithUsername, WithUserID) depends on access to rootfs for resolving via
	// the /etc/{passwd,group} files. So cOpts needs to have precedence over opts.
	return client.NewContainer(ctx, id, cOpts...)
}

func parseIDMappingOption(stringSlices []string) ([]specs.LinuxIDMapping, error) {
	var res []specs.LinuxIDMapping
	for _, str := range stringSlices {
		m, err := parseIDMapping(str)
		if err != nil {
			return nil, err
		}
		res = append(res, m)
	}
	return res, nil
}

func parseIDMapping(mapping string) (specs.LinuxIDMapping, error) {
	// We expect 3 parts, but limit to 4 to allow detection of invalid values.
	parts := strings.SplitN(mapping, ":", 4)
	if len(parts) != 3 {
		return specs.LinuxIDMapping{}, errors.New("user namespace mappings require the format `container-id:host-id:size`")
	}
	cID, err := strconv.ParseUint(parts[0], 0, 32)
	if err != nil {
		return specs.LinuxIDMapping{}, fmt.Errorf("invalid container id for user namespace remapping: %w", err)
	}
	hID, err := strconv.ParseUint(parts[1], 0, 32)
	if err != nil {
		return specs.LinuxIDMapping{}, fmt.Errorf("invalid host id for user namespace remapping: %w", err)
	}
	size, err := strconv.ParseUint(parts[2], 0, 32)
	if err != nil {
		return specs.LinuxIDMapping{}, fmt.Errorf("invalid size for user namespace remapping: %w", err)
	}
	return specs.LinuxIDMapping{
		ContainerID: uint32(cID),
		HostID:      uint32(hID),
		Size:        uint32(size),
	}, nil
}

func validNamespace(ns string) bool {
	linuxNs := specs.LinuxNamespaceType(ns)
	switch linuxNs {
	case specs.PIDNamespace,
		specs.NetworkNamespace,
		specs.UTSNamespace,
		specs.MountNamespace,
		specs.UserNamespace,
		specs.IPCNamespace,
		specs.CgroupNamespace:
		return true
	default:
		return false
	}
}

func getNetNSPath(_ context.Context, task containerd.Task) (string, error) {
	return fmt.Sprintf("/proc/%d/ns/net", task.Pid()), nil
}

// withStaticCDIRegistry inits the CDI registry and disables auto-refresh.
// This is used from the `run` command to avoid creating a registry with auto-refresh enabled.
// It also provides a way to override the CDI spec file paths if required.
func withStaticCDIRegistry() oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		_ = cdi.Configure(cdi.WithAutoRefresh(false))
		if err := cdi.Refresh(); err != nil {
			// We don't consider registry refresh failure a fatal error.
			// For instance, a dynamically generated invalid CDI Spec file for
			// any particular vendor shouldn't prevent injection of devices of
			// different vendors. CDI itself knows better and it will fail the
			// injection if necessary.
			log.G(ctx).Warnf("CDI registry refresh failed: %v", err)
		}
		return nil
	}
}
