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
		cli.IntFlag{
			Name:  "pid",
			Usage: "pid to kill",
			Value: 0,
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

		pid := context.Int("pid")
		all := context.Bool("all")
		if pid > 0 && all {
			return errors.New("enter a pid or all; not both")
		}

		killRequest := &execution.KillRequest{
			ContainerID: id,
			Signal:      uint32(signal),
			PidOrAll: &execution.KillRequest_Pid{
				Pid: uint32(pid),
			},
		}

		if all {
			killRequest.PidOrAll = &execution.KillRequest_All{
				All: true,
			}
		}

		tasks, err := getTasksService(context)
		if err != nil {
			return err
		}
		_, err = tasks.Kill(gocontext.Background(), killRequest)
		if err != nil {
			return err
		}
		return nil
	},
}
