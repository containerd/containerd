package main

import (
	"os"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var taskAttachCommand = cli.Command{
	Name:      "attach",
	Usage:     "attach to the IO of a running container",
	ArgsUsage: "CONTAINER",
	Action: func(context *cli.Context) error {
		ctx, cancel := appContext(context)
		defer cancel()
		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := client.LoadContainer(ctx, context.Args().First())
		if err != nil {
			return err
		}
		spec, err := container.Spec()
		if err != nil {
			return err
		}
		var (
			con console.Console
			tty = spec.Process.Terminal
		)
		if tty {
			con = console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}
		task, err := container.Task(ctx, containerd.WithAttach(os.Stdin, os.Stdout, os.Stderr))
		if err != nil {
			return err
		}
		defer task.Delete(ctx)

		statusC, err := task.Wait(ctx)
		if err != nil {
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

		ec := <-statusC
		code, _, err := ec.Result()
		if err != nil {
			return err
		}
		if code != 0 {
			return cli.NewExitError("", int(code))
		}
		return nil
	},
}
