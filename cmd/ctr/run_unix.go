// +build !windows

package main

import (
	gocontext "context"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go/v1"
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
		checkpointIndex, err := digest.Parse(raw)
		if err != nil {
			return nil, err
		}
		if checkpointIndex != "" {
			return client.NewContainer(ctx, id, containerd.WithCheckpoint(v1.Descriptor{
				Digest: checkpointIndex,
			}, id))
		}
	}

	var (
		opts  []containerd.SpecOpts
		cOpts []containerd.NewContainerOpts
	)
	cOpts = append(cOpts, containerd.WithContainerLabels(labelArgs(context.StringSlice("label"))))
	if context.Bool("rootfs") {
		opts = append(opts, containerd.WithRootFSPath(ref, context.Bool("readonly")))
	} else {
		image, err := client.GetImage(ctx, ref)
		if err != nil {
			return nil, err
		}
		opts = append(opts, containerd.WithImageConfig(image))
		cOpts = append(cOpts, containerd.WithImage(image))
		cOpts = append(cOpts, containerd.WithSnapshotter(context.String("snapshotter")))
		if context.Bool("readonly") {
			cOpts = append(cOpts, containerd.WithNewSnapshotView(id, image))
		} else {
			cOpts = append(cOpts, containerd.WithNewSnapshot(id, image))
		}
	}
	cOpts = append(cOpts, containerd.WithRuntime(context.String("runtime"), nil))

	opts = append(opts, withEnv(context), withMounts(context))
	if len(args) > 0 {
		opts = append(opts, containerd.WithProcessArgs(args...))
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

func newTask(ctx gocontext.Context, container containerd.Container, checkpoint digest.Digest, tty bool) (containerd.Task, error) {
	if checkpoint == "" {
		io := containerd.Stdio
		if tty {
			io = containerd.StdioTerminal
		}
		return container.NewTask(ctx, io)
	}
	return container.NewTask(ctx, containerd.Stdio, containerd.WithTaskCheckpoint(v1.Descriptor{
		Digest: checkpoint,
	}))
}
