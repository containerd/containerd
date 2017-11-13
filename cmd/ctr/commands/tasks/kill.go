package tasks

import (
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var killCommand = cli.Command{
	Name:      "kill",
	Usage:     "signal a container (default: SIGTERM)",
	ArgsUsage: "[flags] CONTAINER",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "signal, s",
			Value: "SIGTERM",
			Usage: "signal to send to the container",
		},
		cli.StringFlag{
			Name:  "exec-id",
			Usage: "process ID to kill",
		},
		cli.BoolFlag{
			Name:  "all, a",
			Usage: "send signal to all processes inside the container",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		signal, err := commands.ParseSignal(context.String("signal"))
		if err != nil {
			return err
		}
		var (
			all    = context.Bool("all")
			execID = context.String("exec-id")
			opts   []containerd.KillOpts
		)
		if all && execID != "" {
			return errors.New("specify an exec-id or all; not both")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		if all {
			opts = append(opts, containerd.WithKillAll)
		}
		if execID != "" {
			opts = append(opts, containerd.WithKillExecID(execID))
		}
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		return task.Kill(ctx, signal, opts...)
	},
}
