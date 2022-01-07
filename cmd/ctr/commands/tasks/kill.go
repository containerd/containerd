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
	"github.com/moby/sys/signal"
	"github.com/urfave/cli"
)

const defaultSignal = "SIGTERM"

var killCommand = cli.Command{
	Name:      "kill",
	Usage:     "signal a container (default: SIGTERM)",
	ArgsUsage: "[flags] CONTAINER",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "signal, s",
			Value: "",
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
		sig, err := signal.ParseSignal(defaultSignal)
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
		if context.String("signal") != "" {
			sig, err = signal.ParseSignal(context.String("signal"))
			if err != nil {
				return err
			}
		} else {
			sig, err = containerd.GetStopSignal(ctx, container, sig)
			if err != nil {
				return err
			}
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		return task.Kill(ctx, sig, opts...)
	},
}
