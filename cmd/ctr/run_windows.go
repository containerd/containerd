package main

import (
	gocontext "context"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/log"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const pipeRoot = `\\.\pipe`

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
		ref          = context.Args().First()
		id           = context.Args().Get(1)
		args         = context.Args()[2:]
		tty          = context.Bool("tty")
		labelStrings = context.StringSlice("label")
	)

	labels := commands.LabelArgs(labelStrings)

	image, err := client.GetImage(ctx, ref)
	if err != nil {
		return nil, err
	}

	opts := []containerd.SpecOpts{
		containerd.WithImageConfig(image),
		withEnv(context),
		withMounts(context),
		withTTY(tty),
	}
	if len(args) > 0 {
		opts = append(opts, containerd.WithProcessArgs(args...))
	}
	if cwd := context.String("cwd"); cwd != "" {
		opts = append(opts, containerd.WithProcessCwd(cwd))
	}
	return client.NewContainer(ctx, id,
		containerd.WithNewSpec(opts...),
		containerd.WithContainerLabels(labels),
		containerd.WithRuntime(context.String("runtime"), nil),
		containerd.WithImage(image),
		containerd.WithSnapshotter(context.String("snapshotter")),
		containerd.WithNewSnapshot(id, image),
	)
}

func newTask(ctx gocontext.Context, client *containerd.Client, container containerd.Container, _ string, tty, nullIO bool) (containerd.Task, error) {
	io := containerd.Stdio
	if tty {
		io = containerd.StdioTerminal
	}
	if nullIO {
		io = containerd.NullIO
	}
	return container.NewTask(ctx, io)
}
