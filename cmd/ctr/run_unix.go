// +build !windows

package main

import (
	gocontext "context"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func init() {
	runCommand.Flags = append(runCommand.Flags, cli.BoolFlag{
		Name:  "rootfs",
		Usage: "use custom rootfs that is not managed by containerd snapshotter.",
	})
}

func handleConsoleResize(ctx gocontext.Context, task resizer, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
		logrus.WithError(err).Error("resize pty")
	}
	s := make(chan os.Signal, 16)
	signal.Notify(s, unix.SIGWINCH)
	go func() {
		for range s {
			size, err := con.Size()
			if err != nil {
				logrus.WithError(err).Error("get pty size")
				continue
			}
			if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
				logrus.WithError(err).Error("resize pty")
			}
		}
	}()
	return nil
}

func withTTY() containerd.SpecOpts {
	return containerd.WithTTY
}

func setHostNetworking() containerd.SpecOpts {
	return containerd.WithHostNamespace(specs.NetworkNamespace)
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
		opts  []containerd.SpecOpts
		cOpts []containerd.NewContainerOpts
	)
	cOpts = append(cOpts, containerd.WithContainerLabels(labelArgs(context.StringSlice("label"))))
	if context.Bool("rootfs") {
		opts = append(opts, containerd.WithRootFSPath(ref))
	} else {
		image, err := client.GetImage(ctx, ref)
		if err != nil {
			return nil, err
		}
		opts = append(opts, containerd.WithImageConfig(image))
		cOpts = append(cOpts, containerd.WithImage(image))
		cOpts = append(cOpts, containerd.WithSnapshotter(context.String("snapshotter")))
		// Even when "readonly" is set, we don't use KindView snapshot here. (#1495)
		// We pass writable snapshot to the OCI runtime, and the runtime remounts it as read-only,
		// after creating some mount points on demand.
		cOpts = append(cOpts, containerd.WithNewSnapshot(id, image))
	}
	if context.Bool("readonly") {
		opts = append(opts, containerd.WithRootFSReadonly())
	}
	cOpts = append(cOpts, containerd.WithRuntime(context.String("runtime"), nil))

	opts = append(opts, withEnv(context), withMounts(context))
	if len(args) > 0 {
		opts = append(opts, containerd.WithProcessArgs(args...))
	}
	if cwd := context.String("cwd"); cwd != "" {
		opts = append(opts, containerd.WithProcessCwd(cwd))
	}
	if context.Bool("tty") {
		opts = append(opts, withTTY())
	}
	if context.Bool("net-host") {
		opts = append(opts, setHostNetworking(), containerd.WithHostHostsFile, containerd.WithHostResolvconf)
	}
	cOpts = append([]containerd.NewContainerOpts{containerd.WithNewSpec(opts...)}, cOpts...)
	return client.NewContainer(ctx, id, cOpts...)
}

func newTask(ctx gocontext.Context, client *containerd.Client, container containerd.Container, checkpoint string, tty, nullIO bool) (containerd.Task, error) {
	if checkpoint == "" {
		io := containerd.Stdio
		if tty {
			io = containerd.StdioTerminal
		}
		if nullIO {
			io = containerd.NullIO
		}
		return container.NewTask(ctx, io)
	}
	im, err := client.GetImage(ctx, checkpoint)
	if err != nil {
		return nil, err
	}
	return container.NewTask(ctx, containerd.Stdio, containerd.WithTaskCheckpoint(im))
}
