package main

import (
	gocontext "context"
	"fmt"
	"runtime"
	"syscall"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

type resizer interface {
	Resize(ctx gocontext.Context, w, h uint32) error
}

type killer interface {
	Kill(gocontext.Context, syscall.Signal, ...containerd.KillOpts) error
}

func withEnv(context *cli.Context) containerd.SpecOpts {
	return func(_ gocontext.Context, _ *containerd.Client, _ *containers.Container, s *specs.Spec) error {
		env := context.StringSlice("env")
		if len(env) > 0 {
			s.Process.Env = replaceOrAppendEnvValues(s.Process.Env, env)
		}
		return nil
	}
}

func withMounts(context *cli.Context) containerd.SpecOpts {
	return func(_ gocontext.Context, _ *containerd.Client, _ *containers.Container, s *specs.Spec) error {
		for _, mount := range context.StringSlice("mount") {
			m, err := parseMountFlag(mount)
			if err != nil {
				return err
			}
			s.Mounts = append(s.Mounts, m)
		}
		return nil
	}
}

var runCommand = cli.Command{
	Name:      "run",
	Usage:     "run a container",
	ArgsUsage: "Image|RootFS ID [COMMAND] [ARG...]",
	Flags: append([]cli.Flag{
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "allocate a TTY for the container",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "runtime name (io.containerd.runtime.v1.linux, io.containerd.runtime.v1.windows, io.containerd.runtime.v1.com.vmware.linux)",
			Value: fmt.Sprintf("io.containerd.runtime.v1.%s", runtime.GOOS),
		},
		cli.BoolFlag{
			Name:  "readonly",
			Usage: "set the containers filesystem as readonly",
		},
		cli.BoolFlag{
			Name:  "net-host",
			Usage: "enable host networking for the container",
		},
		cli.StringSliceFlag{
			Name:  "mount",
			Usage: "specify additional container mount (ex: type=bind,src=/tmp,dest=/host,options=rbind:ro)",
		},
		cli.StringSliceFlag{
			Name:  "env",
			Usage: "specify additional container environment variables (i.e. FOO=bar)",
		},
		cli.StringSliceFlag{
			Name:  "label",
			Usage: "specify additional labels (foo=bar)",
		},
		cli.BoolFlag{
			Name:  "rm",
			Usage: "remove the container after running",
		},
		cli.StringFlag{
			Name:  "checkpoint",
			Usage: "provide the checkpoint digest to restore the container",
		},
	}, snapshotterFlags...),
	Action: func(context *cli.Context) error {
		var (
			err             error
			checkpointIndex digest.Digest

			ctx, cancel = appContext(context)
			id          = context.Args().Get(1)
			imageRef    = context.Args().First()
			tty         = context.Bool("tty")
		)
		defer cancel()

		if imageRef == "" {
			return errors.New("image ref must be provided")
		}
		if id == "" {
			return errors.New("container id must be provided")
		}
		if raw := context.String("checkpoint"); raw != "" {
			if checkpointIndex, err = digest.Parse(raw); err != nil {
				return err
			}
		}
		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := newContainer(ctx, client, context)
		if err != nil {
			return err
		}
		if context.Bool("rm") {
			defer container.Delete(ctx, containerd.WithSnapshotCleanup)
		}
		task, err := newTask(ctx, container, checkpointIndex, tty)
		if err != nil {
			return err
		}
		defer task.Delete(ctx)

		statusC, err := task.Wait(ctx)
		if err != nil {
			return err
		}

		var con console.Console
		if tty {
			con = console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}
		if err := task.Start(ctx); err != nil {
			return err
		}
		if tty {
			if err := handleConsoleResize(ctx, task, con); err != nil {
				logrus.WithError(err).Error("console resize")
			}
		} else {
			sigc := forwardAllSignals(ctx, task)
			defer stopCatch(sigc)
		}

		status := <-statusC
		code, _, err := status.Result()
		if err != nil {
			return err
		}

		if _, err := task.Delete(ctx); err != nil {
			return err
		}
		if code != 0 {
			return cli.NewExitError("", int(code))
		}
		return nil
	},
}
