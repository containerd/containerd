package main

import (
	gocontext "context"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const pipeRoot = `\\.\pipe`

func init() {
	runCommand.Flags = append(runCommand.Flags, cli.StringSliceFlag{
		Name:  "layer",
		Usage: "HCSSHIM Layers to be used",
	})
}

func withLayers(context *cli.Context) containerd.SpecOpts {
	return func(ctx gocontext.Context, client *containerd.Client, c *containers.Container, s *specs.Spec) error {
		l := context.StringSlice("layer")
		if l == nil {
			return errors.Wrap(errdefs.ErrInvalidArgument, "base layers must be specified with `--layer`")
		}
		s.Windows.LayerFolders = l
		return nil
	}
}

func handleConsoleResize(ctx gocontext.Context, task resizer, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	go func() {
		prevSize := size
		for {
			time.Sleep(time.Millisecond * 250)

			size, err := con.Size()
			if err != nil {
				log.G(ctx).WithError(err).Error("get pty size")
				continue
			}

			if size.Width != prevSize.Width || size.Height != prevSize.Height {
				if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
					logrus.WithError(err).Error("resize pty")
				}
				prevSize = size
			}
		}
	}()
	return nil
}

func withTTY(terminal bool) containerd.SpecOpts {
	if !terminal {
		return func(ctx gocontext.Context, client *containerd.Client, c *containers.Container, s *specs.Spec) error {
			s.Process.Terminal = false
			return nil
		}
	}

	con := console.Current()
	size, err := con.Size()
	if err != nil {
		logrus.WithError(err).Error("console size")
	}
	return containerd.WithTTY(int(size.Width), int(size.Height))
}

func setHostNetworking() containerd.SpecOpts {
	return nil
}

func newContainer(ctx gocontext.Context, client *containerd.Client, context *cli.Context) (containerd.Container, error) {
	var (
		// ref          = context.Args().First()
		id           = context.Args().Get(1)
		args         = context.Args()[2:]
		tty          = context.Bool("tty")
		labelStrings = context.StringSlice("label")
	)

	labels := labelArgs(labelStrings)

	// TODO(mlaventure): get base image once we have a snapshotter

	opts := []containerd.SpecOpts{
		// TODO(mlaventure): use containerd.WithImageConfig once we have a snapshotter
		withLayers(context),
		withEnv(context),
		withMounts(context),
		withTTY(tty),
	}
	if len(args) > 0 {
		opts = append(opts, containerd.WithProcessArgs(args...))
	}

	return client.NewContainer(ctx, id,
		containerd.WithNewSpec(opts...),
		containerd.WithContainerLabels(labels),
		containerd.WithRuntime(context.String("runtime"), nil),
		// TODO(mlaventure): containerd.WithImage(image),
	)
}

func newTask(ctx gocontext.Context, container containerd.Container, _ digest.Digest, tty bool) (containerd.Task, error) {
	io := containerd.Stdio
	if tty {
		io = containerd.StdioTerminal
	}
	return container.NewTask(ctx, io)
}
