//go:build !windows

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

package shim

import (
	gocontext "context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/console"
	"github.com/containerd/containerd/v2/api/runtime/task/v2"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/namespaces"
	ptypes "github.com/containerd/containerd/v2/protobuf/types"
	"github.com/containerd/containerd/v2/runtime/v2/shim"
	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
	"github.com/containerd/typeurl/v2"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

var fifoFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "stdin",
		Usage: "Specify the path to the stdin fifo",
	},
	cli.StringFlag{
		Name:  "stdout",
		Usage: "Specify the path to the stdout fifo",
	},
	cli.StringFlag{
		Name:  "stderr",
		Usage: "Specify the path to the stderr fifo",
	},
	cli.BoolFlag{
		Name:  "tty,t",
		Usage: "Enable tty support",
	},
}

// Command is the cli command for interacting with a task
var Command = cli.Command{
	Name:  "shim",
	Usage: "Interact with a shim directly",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "id",
			Usage: "Container id",
		},
	},
	Subcommands: []cli.Command{
		deleteCommand,
		execCommand,
		startCommand,
		stateCommand,
	},
}

var startCommand = cli.Command{
	Name:  "start",
	Usage: "Start a container with a task",
	Action: func(context *cli.Context) error {
		service, err := getTaskService(context)
		if err != nil {
			return err
		}
		_, err = service.Start(gocontext.Background(), &task.StartRequest{
			ID: context.Args().First(),
		})
		return err
	},
}

var deleteCommand = cli.Command{
	Name:  "delete",
	Usage: "Delete a container with a task",
	Action: func(context *cli.Context) error {
		service, err := getTaskService(context)
		if err != nil {
			return err
		}
		r, err := service.Delete(gocontext.Background(), &task.DeleteRequest{
			ID: context.Args().First(),
		})
		if err != nil {
			return err
		}
		fmt.Printf("container deleted and returned exit status %d\n", r.ExitStatus)
		return nil
	},
}

var stateCommand = cli.Command{
	Name:  "state",
	Usage: "Get the state of all the processes of the task",
	Action: func(context *cli.Context) error {
		service, err := getTaskService(context)
		if err != nil {
			return err
		}
		r, err := service.State(gocontext.Background(), &task.StateRequest{
			ID: context.GlobalString("id"),
		})
		if err != nil {
			return err
		}
		commands.PrintAsJSON(r)
		return nil
	},
}

var execCommand = cli.Command{
	Name:  "exec",
	Usage: "Exec a new process in the task's container",
	Flags: append(fifoFlags,
		cli.BoolFlag{
			Name:  "attach,a",
			Usage: "Stay attached to the container and open the fifos",
		},
		cli.StringSliceFlag{
			Name:  "env,e",
			Usage: "Add environment vars",
			Value: &cli.StringSlice{},
		},
		cli.StringFlag{
			Name:  "cwd",
			Usage: "Current working directory",
		},
		cli.StringFlag{
			Name:  "spec",
			Usage: "Runtime spec",
		},
	),
	Action: func(context *cli.Context) error {
		service, err := getTaskService(context)
		if err != nil {
			return err
		}
		var (
			id  = context.Args().First()
			ctx = gocontext.Background()
		)

		if id == "" {
			return errors.New("exec id must be provided")
		}

		tty := context.Bool("tty")
		wg, err := prepareStdio(context.String("stdin"), context.String("stdout"), context.String("stderr"), tty)
		if err != nil {
			return err
		}

		// read spec file and extract Any object
		spec, err := os.ReadFile(context.String("spec"))
		if err != nil {
			return err
		}
		url, err := typeurl.TypeURL(specs.Process{})
		if err != nil {
			return err
		}

		rq := &task.ExecProcessRequest{
			ID: id,
			Spec: &ptypes.Any{
				TypeUrl: url,
				Value:   spec,
			},
			Stdin:    context.String("stdin"),
			Stdout:   context.String("stdout"),
			Stderr:   context.String("stderr"),
			Terminal: tty,
		}
		if _, err := service.Exec(ctx, rq); err != nil {
			return err
		}
		r, err := service.Start(ctx, &task.StartRequest{
			ID: id,
		})
		if err != nil {
			return err
		}
		fmt.Printf("exec running with pid %d\n", r.Pid)
		if context.Bool("attach") {
			log.L.Info("attaching")
			if tty {
				current := console.Current()
				defer current.Reset()
				if err := current.SetRaw(); err != nil {
					return err
				}
				size, err := current.Size()
				if err != nil {
					return err
				}
				if _, err := service.ResizePty(ctx, &task.ResizePtyRequest{
					ID:     id,
					Width:  uint32(size.Width),
					Height: uint32(size.Height),
				}); err != nil {
					return err
				}
			}
			wg.Wait()
		}
		return nil
	},
}

func getTaskService(context *cli.Context) (task.TaskService, error) {
	id := context.GlobalString("id")
	if id == "" {
		return nil, fmt.Errorf("container id must be specified")
	}
	ns := context.GlobalString("namespace")

	// /containerd-shim/ns/id/shim.sock is the old way to generate shim socket,
	// compatible it
	s1 := filepath.Join(string(filepath.Separator), "containerd-shim", ns, id, "shim.sock")
	// this should not error, ctr always get a default ns
	ctx := namespaces.WithNamespace(gocontext.Background(), ns)
	s2, _ := shim.SocketAddress(ctx, context.GlobalString("address"), id)
	s2 = strings.TrimPrefix(s2, "unix://")

	for _, socket := range []string{s2, "\x00" + s1} {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			client := ttrpc.NewClient(conn)

			// TODO(stevvooe): This actually leaks the connection. We were leaking it
			// before, so may not be a huge deal.

			return task.NewTaskClient(client), nil
		}
	}

	return nil, fmt.Errorf("fail to connect to container %s's shim", id)
}
