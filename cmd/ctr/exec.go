package main

import (
	"errors"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/urfave/cli"
)

var execCommand = cli.Command{
	Name:      "exec",
	Usage:     "execute additional processes in an existing container",
	ArgsUsage: "CONTAINER CMD [ARG...]",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "cwd",
			Usage: "working directory of the new process",
		},
		cli.BoolFlag{
			Name:  "tty,t",
			Usage: "allocate a TTY for the container",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx, cancel = appContext(context)
			id          = context.Args().First()
			args        = context.Args().Tail()
			tty         = context.Bool("tty")
		)
		defer cancel()

		if id == "" {
			return errors.New("container id must be provided")
		}
		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		spec, err := container.Spec()
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}

		pspec := spec.Process
		pspec.Terminal = tty
		pspec.Args = args

		io := containerd.Stdio
		if tty {
			io = containerd.StdioTerminal
		}
		process, err := task.Exec(ctx, pspec, io)
		if err != nil {
			return err
		}
		defer process.Delete(ctx)

		statusC := make(chan uint32, 1)
		go func() {
			status, err := process.Wait(ctx)
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
		if err := process.Start(ctx); err != nil {
			return err
		}
		if tty {
			if err := handleConsoleResize(ctx, process, con); err != nil {
				logrus.WithError(err).Error("console resize")
			}
		} else {
			sigc := forwardAllSignals(ctx, process)
			defer stopCatch(sigc)
		}
		status := <-statusC
		if status != 0 {
			return cli.NewExitError("", int(status))
		}
		return nil
	},
}
