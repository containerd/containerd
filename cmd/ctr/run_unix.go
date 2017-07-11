// +build !windows

package main

import (
	gocontext "context"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	"github.com/containerd/containerd"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

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
		err             error
		checkpointIndex digest.Digest

		ref          = context.Args().First()
		id           = context.Args().Get(1)
		args         = context.Args()[2:]
		tty          = context.Bool("tty")
		labelStrings = context.StringSlice("label")
	)

	labels := labelArgs(labelStrings)

	if raw := context.String("checkpoint"); raw != "" {
		if checkpointIndex, err = digest.Parse(raw); err != nil {
			return nil, err
		}
	}
	image, err := client.GetImage(ctx, ref)
	if err != nil {
		return nil, err
	}

	if checkpointIndex == "" {
		opts := []containerd.SpecOpts{
			containerd.WithImageConfig(ctx, image),
			withEnv(context),
			withMounts(context),
		}
		if len(args) > 0 {
			opts = append(opts, containerd.WithProcessArgs(args...))
		}
		if tty {
			opts = append(opts, withTTY())
		}
		if context.Bool("net-host") {
			opts = append(opts, setHostNetworking())
		}
		spec, err := containerd.GenerateSpec(opts...)
		if err != nil {
			return nil, err
		}
		var rootfs containerd.NewContainerOpts
		if context.Bool("readonly") {
			rootfs = containerd.WithNewReadonlyRootFS(id, image)
		} else {
			rootfs = containerd.WithNewRootFS(id, image)
		}

		return client.NewContainer(ctx, id,
			containerd.WithSpec(spec),
			containerd.WithImage(image),
			containerd.WithContainerLabels(labels),
			containerd.WithSnapshotter(context.String("snapshotter")),
			rootfs,
		)
	}

	return client.NewContainer(ctx, id, containerd.WithCheckpoint(v1.Descriptor{
		Digest: checkpointIndex,
	}, id))
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
