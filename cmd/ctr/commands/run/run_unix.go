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
	gocontext "context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/container-orchestrated-devices/container-device-interface/pkg/cdi"
	"github.com/container-orchestrated-devices/container-device-interface/pkg/parser"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/containers"
	"github.com/containerd/containerd/v2/contrib/apparmor"
	"github.com/containerd/containerd/v2/contrib/nvidia"
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/oci"
	runtimeoptions "github.com/containerd/containerd/v2/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/runtime/v2/runc/options"
	"github.com/containerd/containerd/v2/snapshots"
	"github.com/containerd/log"
	"github.com/intel/goresctrl/pkg/blockio"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

var platformRunFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "runc-binary",
		Usage: "Specify runc-compatible binary",
	},
	cli.StringFlag{
		Name:  "runc-root",
		Usage: "Specify runc-compatible root",
	},
	cli.BoolFlag{
		Name:  "runc-systemd-cgroup",
		Usage: "Start runc with systemd cgroup manager",
	},
	cli.StringFlag{
		Name:  "uidmap",
		Usage: "Run inside a user namespace with the specified UID mapping range; specified with the format `container-uid:host-uid:length`",
	},
	cli.StringFlag{
		Name:  "gidmap",
		Usage: "Run inside a user namespace with the specified GID mapping range; specified with the format `container-gid:host-gid:length`",
	},
	cli.BoolFlag{
		Name:  "remap-labels",
		Usage: "Provide the user namespace ID remapping to the snapshotter via label options; requires snapshotter support",
	},
	cli.BoolFlag{
		Name:  "privileged-without-host-devices",
		Usage: "Don't pass all host devices to privileged container",
	},
	cli.Float64Flag{
		Name:  "cpus",
		Usage: "Set the CFS cpu quota",
		Value: 0.0,
	},
	cli.IntFlag{
		Name:  "cpu-shares",
		Usage: "Set the cpu shares",
		Value: 1024,
	},
	cli.StringFlag{
		Name:  "cpuset-cpus",
		Usage: "Set the CPUs the container will run in (e.g., 1-2,4)",
	},
	cli.StringFlag{
		Name:  "cpuset-mems",
		Usage: "Set the memory nodes the container will run in (e.g., 1-2,4)",
	},
}

// NewContainer creates a new container
func NewContainer(ctx gocontext.Context, client *containerd.Client, context *cli.Context) (containerd.Container, error) {
	var (
		id     string
		config = context.IsSet("config")
	)
	if config {
		id = context.Args().First()
	} else {
		id = context.Args().Get(1)
	}

	var (
		opts  []oci.SpecOpts
		cOpts []containerd.NewContainerOpts
		spec  containerd.NewContainerOpts
	)

	if sandbox := context.String("sandbox"); sandbox != "" {
		cOpts = append(cOpts, containerd.WithSandbox(sandbox))
	}

	if config {
		cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(context.StringSlice("label"))))
		opts = append(opts, oci.WithSpecFromFile(context.String("config")))
	} else {
		var (
			ref = context.Args().First()
			// for container's id is Args[1]
			args = context.Args()[2:]
		)
		opts = append(opts, oci.WithDefaultSpec(), oci.WithDefaultUnixDevices)
		if ef := context.String("env-file"); ef != "" {
			opts = append(opts, oci.WithEnvFile(ef))
		}
		opts = append(opts, oci.WithEnv(context.StringSlice("env")))
		opts = append(opts, withMounts(context))

		if context.Bool("rootfs") {
			rootfs, err := filepath.Abs(ref)
			if err != nil {
				return nil, err
			}
			opts = append(opts, oci.WithRootFSPath(rootfs))
			cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(context.StringSlice("label"))))
		} else {
			snapshotter := context.String("snapshotter")
			var image containerd.Image
			i, err := client.ImageService().Get(ctx, ref)
			if err != nil {
				return nil, err
			}
			if ps := context.String("platform"); ps != "" {
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
				if err := image.Unpack(ctx, snapshotter); err != nil {
					return nil, err
				}
			}
			labels := buildLabels(commands.LabelArgs(context.StringSlice("label")), image.Labels())
			opts = append(opts, oci.WithImageConfig(image))
			cOpts = append(cOpts,
				containerd.WithImage(image),
				containerd.WithImageConfigLabels(image),
				containerd.WithAdditionalContainerLabels(labels),
				containerd.WithSnapshotter(snapshotter))
			if uidmap, gidmap := context.String("uidmap"), context.String("gidmap"); uidmap != "" && gidmap != "" {
				uidMap, err := parseIDMapping(uidmap)
				if err != nil {
					return nil, err
				}
				gidMap, err := parseIDMapping(gidmap)
				if err != nil {
					return nil, err
				}
				opts = append(opts,
					oci.WithUserNamespace([]specs.LinuxIDMapping{uidMap}, []specs.LinuxIDMapping{gidMap}))
				// use snapshotter opts or the remapped snapshot support to shift the filesystem
				// currently the snapshotters known to support the labels are:
				// fuse-overlayfs - https://github.com/containerd/fuse-overlayfs-snapshotter
				// overlay - in case of idmapped mount points are supported by host kernel (Linux kernel 5.19)
				if context.Bool("remap-labels") {
					cOpts = append(cOpts, containerd.WithNewSnapshot(id, image,
						containerd.WithRemapperLabels(0, uidMap.HostID, 0, gidMap.HostID, uidMap.Size)))
				} else {
					cOpts = append(cOpts, containerd.WithRemappedSnapshot(id, image, uidMap.HostID, gidMap.HostID))
				}
			} else {
				// Even when "read-only" is set, we don't use KindView snapshot here. (#1495)
				// We pass writable snapshot to the OCI runtime, and the runtime remounts it as read-only,
				// after creating some mount points on demand.
				// For some snapshotter, such as overlaybd, it can provide 2 kind of writable snapshot(overlayfs dir or block-device)
				// by command label values.
				cOpts = append(cOpts, containerd.WithNewSnapshot(id, image,
					snapshots.WithLabels(commands.LabelArgs(context.StringSlice("snapshotter-label")))))
			}
			cOpts = append(cOpts, containerd.WithImageStopSignal(image, "SIGTERM"))
		}
		if context.Bool("read-only") {
			opts = append(opts, oci.WithRootFSReadonly())
		}
		if len(args) > 0 {
			opts = append(opts, oci.WithProcessArgs(args...))
		}
		if cwd := context.String("cwd"); cwd != "" {
			opts = append(opts, oci.WithProcessCwd(cwd))
		}
		if user := context.String("user"); user != "" {
			opts = append(opts, oci.WithUser(user), oci.WithAdditionalGIDs(user))
		}
		if context.Bool("tty") {
			opts = append(opts, oci.WithTTY)
		}

		privileged := context.Bool("privileged")
		privilegedWithoutHostDevices := context.Bool("privileged-without-host-devices")
		if privilegedWithoutHostDevices && !privileged {
			return nil, fmt.Errorf("can't use 'privileged-without-host-devices' without 'privileged' specified")
		}
		if privileged {
			if privilegedWithoutHostDevices {
				opts = append(opts, oci.WithPrivileged)
			} else {
				opts = append(opts, oci.WithPrivileged, oci.WithAllDevicesAllowed, oci.WithHostDevices)
			}
		}

		if context.Bool("net-host") {
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
		if annoStrings := context.StringSlice("annotation"); len(annoStrings) > 0 {
			annos, err := commands.AnnotationArgs(annoStrings)
			if err != nil {
				return nil, err
			}
			opts = append(opts, oci.WithAnnotations(annos))
		}

		if caps := context.StringSlice("cap-add"); len(caps) > 0 {
			for _, cap := range caps {
				if !strings.HasPrefix(cap, "CAP_") {
					return nil, fmt.Errorf("capabilities must be specified with 'CAP_' prefix")
				}
			}
			opts = append(opts, oci.WithAddedCapabilities(caps))
		}

		if caps := context.StringSlice("cap-drop"); len(caps) > 0 {
			for _, cap := range caps {
				if !strings.HasPrefix(cap, "CAP_") {
					return nil, fmt.Errorf("capabilities must be specified with 'CAP_' prefix")
				}
			}
			opts = append(opts, oci.WithDroppedCapabilities(caps))
		}

		seccompProfile := context.String("seccomp-profile")

		if !context.Bool("seccomp") && seccompProfile != "" {
			return nil, fmt.Errorf("seccomp must be set to true, if using a custom seccomp-profile")
		}

		if context.Bool("seccomp") {
			if seccompProfile != "" {
				opts = append(opts, seccomp.WithProfile(seccompProfile))
			} else {
				opts = append(opts, seccomp.WithDefaultProfile())
			}
		}

		if s := context.String("apparmor-default-profile"); len(s) > 0 {
			opts = append(opts, apparmor.WithDefaultProfile(s))
		}

		if s := context.String("apparmor-profile"); len(s) > 0 {
			if len(context.String("apparmor-default-profile")) > 0 {
				return nil, fmt.Errorf("apparmor-profile conflicts with apparmor-default-profile")
			}
			opts = append(opts, apparmor.WithProfile(s))
		}

		if cpus := context.Float64("cpus"); cpus > 0.0 {
			var (
				period = uint64(100000)
				quota  = int64(cpus * 100000.0)
			)
			opts = append(opts, oci.WithCPUCFS(quota, period))
		}

		if cpusetCpus := context.String("cpuset-cpus"); len(cpusetCpus) > 0 {
			opts = append(opts, oci.WithCPUs(cpusetCpus))
		}

		if cpusetMems := context.String("cpuset-mems"); len(cpusetMems) > 0 {
			opts = append(opts, oci.WithCPUsMems(cpusetMems))
		}

		if shares := context.Int("cpu-shares"); shares > 0 {
			opts = append(opts, oci.WithCPUShares(uint64(shares)))
		}

		quota := context.Int64("cpu-quota")
		period := context.Uint64("cpu-period")
		if quota != -1 || period != 0 {
			if cpus := context.Float64("cpus"); cpus > 0.0 {
				return nil, errors.New("cpus and quota/period should be used separately")
			}
			opts = append(opts, oci.WithCPUCFS(quota, period))
		}

		if burst := context.Uint64("cpu-burst"); burst != 0 {
			opts = append(opts, oci.WithCPUBurst(burst))
		}

		joinNs := context.StringSlice("with-ns")
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
		if context.IsSet("gpus") {
			opts = append(opts, nvidia.WithGPUs(nvidia.WithDevices(context.IntSlice("gpus")...), nvidia.WithAllCapabilities))
		}
		if context.IsSet("allow-new-privs") {
			opts = append(opts, oci.WithNewPrivileges)
		}
		if context.IsSet("cgroup") {
			// NOTE: can be set to "" explicitly for disabling cgroup.
			opts = append(opts, oci.WithCgroup(context.String("cgroup")))
		}
		limit := context.Uint64("memory-limit")
		if limit != 0 {
			opts = append(opts, oci.WithMemoryLimit(limit))
		}
		var cdiDeviceIDs []string
		for _, dev := range context.StringSlice("device") {
			if parser.IsQualifiedName(dev) {
				cdiDeviceIDs = append(cdiDeviceIDs, dev)
				continue
			}
			opts = append(opts, oci.WithDevices(dev, "", "rwm"))
		}
		if len(cdiDeviceIDs) > 0 {
			opts = append(opts, withStaticCDIRegistry())
		}
		opts = append(opts, oci.WithCDIDevices(cdiDeviceIDs...))

		rootfsPropagation := context.String("rootfs-propagation")
		if rootfsPropagation != "" {
			opts = append(opts, func(_ gocontext.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
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

		if c := context.String("blockio-config-file"); c != "" {
			if err := blockio.SetConfigFromFile(c, false); err != nil {
				return nil, fmt.Errorf("blockio-config-file error: %w", err)
			}
		}

		if c := context.String("blockio-class"); c != "" {
			if linuxBlockIO, err := blockio.OciLinuxBlockIO(c); err == nil {
				opts = append(opts, oci.WithBlockIO(linuxBlockIO))
			} else {
				return nil, fmt.Errorf("blockio-class error: %w", err)
			}
		}
		if c := context.String("rdt-class"); c != "" {
			opts = append(opts, oci.WithRdt(c, "", ""))
		}
		if hostname := context.String("hostname"); hostname != "" {
			opts = append(opts, oci.WithHostname(hostname))
		}
	}

	if context.Bool("cni") {
		cniMeta := &commands.NetworkMetaData{EnableCni: true}
		cOpts = append(cOpts, containerd.WithContainerExtension(commands.CtrCniMetadataExtension, cniMeta))
	}

	runtimeOpts, err := getRuntimeOptions(context)
	if err != nil {
		return nil, err
	}
	cOpts = append(cOpts, containerd.WithRuntime(context.String("runtime"), runtimeOpts))

	opts = append(opts, oci.WithAnnotations(commands.LabelArgs(context.StringSlice("label"))))
	var s specs.Spec
	spec = containerd.WithSpec(&s, opts...)

	cOpts = append(cOpts, spec)

	// oci.WithImageConfig (WithUsername, WithUserID) depends on access to rootfs for resolving via
	// the /etc/{passwd,group} files. So cOpts needs to have precedence over opts.
	return client.NewContainer(ctx, id, cOpts...)
}

func getRuncOptions(context *cli.Context) (*options.Options, error) {
	runtimeOpts := &options.Options{}
	if runcBinary := context.String("runc-binary"); runcBinary != "" {
		runtimeOpts.BinaryName = runcBinary
	}
	if context.Bool("runc-systemd-cgroup") {
		if context.String("cgroup") == "" {
			// runc maps "machine.slice:foo:deadbeef" to "/machine.slice/foo-deadbeef.scope"
			return nil, errors.New("option --runc-systemd-cgroup requires --cgroup to be set, e.g. \"machine.slice:foo:deadbeef\"")
		}
		runtimeOpts.SystemdCgroup = true
	}
	if root := context.String("runc-root"); root != "" {
		runtimeOpts.Root = root
	}

	return runtimeOpts, nil
}

func getRuntimeOptions(context *cli.Context) (interface{}, error) {
	// validate first
	if (context.String("runc-binary") != "" || context.Bool("runc-systemd-cgroup")) &&
		context.String("runtime") != "io.containerd.runc.v2" {
		return nil, errors.New("specifying runc-binary and runc-systemd-cgroup is only supported for \"io.containerd.runc.v2\" runtime")
	}

	if context.String("runtime") == "io.containerd.runc.v2" {
		return getRuncOptions(context)
	}

	if configPath := context.String("runtime-config-path"); configPath != "" {
		return &runtimeoptions.Options{
			ConfigPath: configPath,
		}, nil
	}

	return nil, nil
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

func getNetNSPath(_ gocontext.Context, task containerd.Task) (string, error) {
	return fmt.Sprintf("/proc/%d/ns/net", task.Pid()), nil
}

// withStaticCDIRegistry inits the CDI registry and disables auto-refresh.
// This is used from the `run` command to avoid creating a registry with auto-refresh enabled.
// It also provides a way to override the CDI spec file paths if required.
func withStaticCDIRegistry() oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		registry := cdi.GetRegistry(cdi.WithAutoRefresh(false))
		if err := registry.Refresh(); err != nil {
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
