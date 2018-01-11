// +build !windows

package run

import (
	gocontext "context"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

func init() {
	Command.Flags = append(Command.Flags, cli.BoolFlag{
		Name:  "rootfs",
		Usage: "use custom rootfs that is not managed by containerd snapshotter",
	})
}

func newContainer(ctx gocontext.Context, client *containerd.Client, context *cli.Context) (containerd.Container, error) {
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
	)
	cOpts = append(cOpts, containerd.WithContainerLabels(commands.LabelArgs(context.StringSlice("label"))))
	if context.Bool("rootfs") {
		opts = append(opts, oci.WithRootFSPath(ref))
	} else {
		image, err := client.GetImage(ctx, ref)
		if err != nil {
			return nil, err
		}
		opts = append(opts, oci.WithImageConfig(image))
		cOpts = append(cOpts, containerd.WithImage(image))
		cOpts = append(cOpts, containerd.WithSnapshotter(context.String("snapshotter")))
		// Even when "readonly" is set, we don't use KindView snapshot here. (#1495)
		// We pass writable snapshot to the OCI runtime, and the runtime remounts it as read-only,
		// after creating some mount points on demand.
		cOpts = append(cOpts, containerd.WithNewSnapshot(id, image))
	}
	if context.Bool("readonly") {
		opts = append(opts, oci.WithRootFSReadonly())
	}
	cOpts = append(cOpts, containerd.WithRuntime(context.String("runtime"), nil))

	opts = append(opts, withEnv(context), withMounts(context))
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
	// oci.WithImageConfig (WithUsername, WithUserID) depends on rootfs snapshot for resolving /etc/passwd.
	// So cOpts needs to have precedence over opts.
	// TODO: WithUsername, WithUserID should additionally support non-snapshot rootfs
	cOpts = append(cOpts, []containerd.NewContainerOpts{containerd.WithNewSpec(opts...)}...)
	return client.NewContainer(ctx, id, cOpts...)
}
