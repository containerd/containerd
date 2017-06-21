package main

import (
	gocontext "context"
	"syscall"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	"github.com/containerd/containerd"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

type resizer interface {
	Resize(ctx gocontext.Context, w, h uint32) error
}

type killer interface {
	Kill(gocontext.Context, syscall.Signal) error
}

func withEnv(context *cli.Context) containerd.SpecOpts {
	return func(s *specs.Spec) error {
		env := context.StringSlice("env")
		if len(env) > 0 {
			s.Process.Env = append(s.Process.Env, env...)
		}
		return nil
	}
}

func withMounts(context *cli.Context) containerd.SpecOpts {
	return func(s *specs.Spec) error {
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
	ArgsUsage: "IMAGE ID [COMMAND] [ARG...]",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "allocate a TTY for the container",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "runtime name (io.containerd.runtime.v1.linux, io.containerd.runtime.v1.windows, io.containerd.runtime.v1.com.vmware.linux)",
			Value: "io.containerd.runtime.v1.linux",
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
	},
	Action: func(context *cli.Context) error {
		var (
			err             error
			checkpointIndex digest.Digest

			ctx, cancel = appContext(context)
			id          = context.Args().Get(1)
			tty         = context.Bool("tty")
		)
		defer cancel()

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
			defer container.Delete(ctx, containerd.WithRootFSDeletion)
		}
		task, err := newTask(ctx, container, checkpointIndex, tty)
		if err != nil {
			return err
		}
		defer task.Delete(ctx)

		statusC := make(chan uint32, 1)
		go func() {
			status, err := task.Wait(ctx)
			if err != nil {
				logrus.WithError(err).Error("wait process")
			}
			statusC <- status
		}()
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
		if _, err := task.Delete(ctx); err != nil {
			return err
		}
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}
