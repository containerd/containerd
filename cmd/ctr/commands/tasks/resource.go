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
	"errors"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

func init() {
	// resourceCommand is only added on Linux, does not compile on darwin or windows
	Command.Subcommands = append(Command.Subcommands, resourceCommand)
}

var resourceCommand = cli.Command{
	Name:           "resource",
	Usage:          "change resource limits of existing container",
	ArgsUsage:      "[flags] CONTAINER",
	SkipArgReorder: true,
	Flags: []cli.Flag{
		cli.Uint64Flag{
			Name:  "memory-limit",
			Usage: "memory limit (in bytes) for the container",
		},
		cli.Int64Flag{
			Name:  "cpu-quota",
			Usage: "Limit CPU CFS quota",
			Value: -1,
		},
		cli.Uint64Flag{
			Name:  "cpu-period",
			Usage: "Limit CPU CFS period",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			id     = context.Args().First()
			limit  = context.Int64("memory-limit")
			quota  = context.Int64("cpu-quota")
			period = context.Uint64("cpu-period")
		)
		if id == "" {
			return errors.New("container id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}

		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}

		if err := task.Update(ctx, containerd.WithResources(&specs.LinuxResources{
			Memory: &specs.LinuxMemory{
				Limit: &limit,
			},
			CPU: &specs.LinuxCPU{
				Quota:  &quota,
				Period: &period,
			},
		})); err != nil {
			return err
		}

		return nil
	},
}
