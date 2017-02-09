package main

import (
	gocontext "context"
	"strconv"

	"github.com/docker/containerd/api/services/execution"
	"github.com/docker/containerd/api/types/state"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var killCommand = cli.Command{
	Name:  "kill",
	Usage: "kill [OPTIONS] <CONTAINERID> [PID]",
	Flags: []cli.Flag{
		cli.IntFlag{
			Name:  "signal,s",
			Value: 15,
			Usage: "signal to send to the container",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id is required")
		}

		executionService, err := getExecutionService(context)
		if err != nil {
			return err
		}
		_, err = executionService.GetContainer(gocontext.Background(),
			&execution.GetContainerRequest{
				ID: id,
			})
		if err != nil {
			return err
		}

		lpr, err := executionService.ListProcesses(gocontext.Background(),
			&execution.ListProcessesRequest{
				ContainerID: id,
			})
		if err != nil {
			return err
		}

		var pid uint32
		pidStr := context.Args().Get(1)
		if pidStr == "" {
			pid = lpr.InitPid
		} else {
			p, err := strconv.Atoi(pidStr)
			if err != nil {
				return err
			}
			for _, proc := range lpr.Processes {
				if proc.Pid == uint32(p) {
					if proc.State == state.State_STOPPED {
						return errors.Errorf("process %d is stopped", p)
					}
					pid = uint32(p)
					break
				}
			}
			if pid == 0 {
				return errors.Errorf("no such pid %s", pidStr)
			}
		}

		_, err = executionService.SignalProcess(gocontext.Background(),
			&execution.SignalProcessRequest{
				ContainerID: id,
				Pid:         pid,
				Signal:      uint32(context.Int("signal")),
			})

		return err
	},
}
