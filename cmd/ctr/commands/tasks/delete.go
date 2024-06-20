/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package tasks

import (
	"context"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/log"
	"github.com/urfave/cli/v2"
)

var deleteCommand = &cli.Command{
	Name:      "delete",
	Usage:     "Delete one or more tasks",
	ArgsUsage: "CONTAINER [CONTAINER, ...]",
	Aliases:   []string{"del", "remove", "rm"},
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "force",
			Aliases: []string{"f"},
			Usage:   "Force delete task process",
		},
		&cli.StringFlag{
			Name:  "exec-id",
			Usage: "Process ID to kill",
		},
	},
	Action: func(cliContext *cli.Context) error {
		var (
			execID = cliContext.String("exec-id")
			force  = cliContext.Bool("force")
		)
		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()
		var opts []containerd.ProcessDeleteOpts
		if force {
			opts = append(opts, containerd.WithProcessKill)
		}
		var exitErr error
		if execID != "" {
			task, err := loadTask(ctx, client, cliContext.Args().First())
			if err != nil {
				return err
			}
			p, err := task.LoadProcess(ctx, execID, nil)
			if err != nil {
				return err
			}
			status, err := p.Delete(ctx, opts...)
			if err != nil {
				return err
			}
			if ec := status.ExitCode(); ec != 0 {
				return cli.Exit("", int(ec))
			}
		} else {
			for _, target := range cliContext.Args().Slice() {
				task, err := loadTask(ctx, client, target)
				if err != nil {
					if exitErr == nil {
						exitErr = err
					}
					log.G(ctx).WithError(err).Errorf("failed to load task from %v", target)
					continue
				}
				status, err := task.Delete(ctx, opts...)
				if err != nil {
					if exitErr == nil {
						exitErr = err
					}
					log.G(ctx).WithError(err).Errorf("unable to delete %v", task.ID())
					continue
				}
				if ec := status.ExitCode(); ec != 0 {
					log.G(ctx).Warnf("task %v exit with non-zero exit code %v", task.ID(), int(ec))
				}
			}
		}
		return exitErr
	},
}

func loadTask(ctx context.Context, client *containerd.Client, containerID string) (containerd.Task, error) {
	container, err := client.LoadContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}
	task, err := container.Task(ctx, cio.Load)
	if err != nil {
		return nil, err
	}
	return task, nil
}
