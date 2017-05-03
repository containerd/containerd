package main

import (
	gocontext "context"

	"github.com/containerd/containerd/api/services/execution"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var killCommand = cli.Command{
	Name:  "kill",
	Usage: "signal a container (default: SIGTERM)",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "id of the container",
		},
		cli.BoolFlag{
			Name:  "all, a",
			Usage: "send signal to all processes inside the container",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.String("id")
		if id == "" {
			return errors.New("container id must be provided")
		}

		sigstr := context.Args().First()
		if sigstr == "" {
			sigstr = "SIGTERM"
		}

		signal, err := parseSignal(sigstr)
		if err != nil {
			return err
		}

		containers, err := getExecutionService(context)
		if err != nil {
			return err
		}
		killRequest := &execution.KillRequest{
			ID:     id,
			Signal: uint32(signal),
			All:    context.Bool("all"),
		}
		_, err = containers.Kill(gocontext.Background(), killRequest)
		if err != nil {
			return err
		}
		return nil
	},
}
