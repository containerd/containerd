// +build !windows

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
	gocontext "context"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

func init() {
	ContainerFlags = append(ContainerFlags, cli.BoolFlag{
		Name:  "rootfs",
		Usage: "use custom rootfs that is not managed by containerd snapshotter",
	}, cli.BoolFlag{
		Name:  "no-pivot",
		Usage: "disable use of pivot-root (linux only)",
	})
}

// NewContainer creates a new container
func NewContainer(ctx gocontext.Context, client *containerd.Client, context *cli.Context) (containerd.Container, error) {
	var (
		ref  = context.Args().First()
		id   = context.Args().Get(1)
		args = context.Args()[2:]
	)

	if raw := context.String("checkpoint"); raw != "" {
		im, err := client.GetImage(ctx, raw)
		if err != nil {
			return nil, err
		}
		return client.NewContainer(ctx, id, containerd.WithCheckpoint(im, id))
	}

	var (
		opts  []oci.SpecOpts
		cOpts []containerd.NewContainerOpts
		spec  containerd.NewContainerOpts
	)
	opts = append(opts, oci.WithEnv(context.StringSlice("env")))
	opts = append(opts, withMounts(context))
	cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(context.StringSlice("label"))))
	cOpts = append(cOpts, containerd.WithRuntime(context.String("runtime"), nil))
	if context.Bool("rootfs") {
		opts = append(opts, oci.WithRootFSPath(ref))
	} else {
		snapshotter := context.String("snapshotter")
		image, err := client.GetImage(ctx, ref)
		if err != nil {
			return nil, err
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
		opts = append(opts, oci.WithImageConfig(image))
		cOpts = append(cOpts,
			containerd.WithImage(image),
			containerd.WithSnapshotter(snapshotter),
			// Even when "readonly" is set, we don't use KindView snapshot here. (#1495)
			// We pass writable snapshot to the OCI runtime, and the runtime remounts it as read-only,
			// after creating some mount points on demand.
			containerd.WithNewSnapshot(id, image))
	}
	if context.Bool("readonly") {
		opts = append(opts, oci.WithRootFSReadonly())
	}
	if len(args) > 0 {
		opts = append(opts, oci.WithProcessArgs(args...))
	}
	if cwd := context.String("cwd"); cwd != "" {
		opts = append(opts, oci.WithProcessCwd(cwd))
	}
	if context.Bool("tty") {
		opts = append(opts, oci.WithTTY)
	}
	if context.Bool("net-host") {
		opts = append(opts, oci.WithHostNamespace(specs.NetworkNamespace), oci.WithHostHostsFile, oci.WithHostResolvconf)
	}
	joinNs := context.StringSlice("with-ns")
	for _, ns := range joinNs {
		parts := strings.Split(ns, ":")
		if len(parts) != 2 {
			return nil, errors.New("joining a Linux namespace using --with-ns requires the format 'nstype:path'")
		}
		if !validNamespace(parts[0]) {
			return nil, errors.New("the Linux namespace type specified in --with-ns is not valid: " + parts[0])
		}
		opts = append(opts, oci.WithLinuxNamespace(specs.LinuxNamespace{
			Type: specs.LinuxNamespaceType(parts[0]),
			Path: parts[1],
		}))
	}
	if context.IsSet("config") {
		var s specs.Spec
		if err := loadSpec(context.String("config"), &s); err != nil {
			return nil, err
		}
		spec = containerd.WithSpec(&s, opts...)
	} else {
		spec = containerd.WithNewSpec(opts...)
	}
	cOpts = append(cOpts, spec)

	// oci.WithImageConfig (WithUsername, WithUserID) depends on rootfs snapshot for resolving /etc/passwd.
	// So cOpts needs to have precedence over opts.
	// TODO: WithUsername, WithUserID should additionally support non-snapshot rootfs
	return client.NewContainer(ctx, id, cOpts...)
}

func getNewTaskOpts(context *cli.Context) []containerd.NewTaskOpts {
	if context.Bool("no-pivot") {
		return []containerd.NewTaskOpts{containerd.WithNoPivotRoot}
	}
	return nil
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
