// +build !windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	gocontext "context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/console"
	"github.com/containerd/containerd/api/services/shim"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var fifoFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "stdin",
		Usage: "specify the path to the stdin fifo",
	},
	cli.StringFlag{
		Name:  "stdout",
		Usage: "specify the path to the stdout fifo",
	},
	cli.StringFlag{
		Name:  "stderr",
		Usage: "specify the path to the stderr fifo",
	},
	cli.BoolFlag{
		Name:  "tty,t",
		Usage: "enable tty support",
	},
}

var shimCommand = cli.Command{
	Name:  "shim",
	Usage: "interact with a shim directly",
	Subcommands: []cli.Command{
		shimCreateCommand,
		shimStartCommand,
		shimDeleteCommand,
		shimEventsCommand,
		shimStateCommand,
		shimExecCommand,
	},
}

var shimCreateCommand = cli.Command{
	Name:  "create",
	Usage: "create a container with a shim",
	Flags: append(fifoFlags,
		cli.StringFlag{
			Name:  "bundle",
			Usage: "bundle path for the container",
		},
		cli.StringFlag{
			Name:  "runtime",
			Value: "runc",
			Usage: "runtime to use for the container",
		},
		cli.BoolFlag{
			Name:  "attach,a",
			Usage: "stay attached to the container and open the fifos",
		},
	),
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		service, err := getShimService()
		if err != nil {
			return err
		}
		tty := context.Bool("tty")
		wg, err := prepareStdio(context.String("stdin"), context.String("stdout"), context.String("stderr"), tty)
		if err != nil {
			return err
		}
		r, err := service.Create(gocontext.Background(), &shim.CreateRequest{
			ID:       id,
			Bundle:   context.String("bundle"),
			Runtime:  context.String("runtime"),
			Stdin:    context.String("stdin"),
			Stdout:   context.String("stdout"),
			Stderr:   context.String("stderr"),
			Terminal: tty,
		})
		if err != nil {
			return err
		}
		fmt.Printf("container created with id %s and pid %d\n", id, r.Pid)
		if context.Bool("attach") {
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
				if _, err := service.Pty(gocontext.Background(), &shim.PtyRequest{
					Pid:    r.Pid,
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

var shimStartCommand = cli.Command{
	Name:  "start",
	Usage: "start a container with a shim",
	Action: func(context *cli.Context) error {
		service, err := getShimService()
		if err != nil {
			return err
		}
		_, err = service.Start(gocontext.Background(), &shim.StartRequest{})
		return err
	},
}

var shimDeleteCommand = cli.Command{
	Name:  "delete",
	Usage: "delete a container with a shim",
	Action: func(context *cli.Context) error {
		service, err := getShimService()
		if err != nil {
			return err
		}
		pid, err := strconv.Atoi(context.Args().First())
		if err != nil {
			return err
		}
		r, err := service.Delete(gocontext.Background(), &shim.DeleteRequest{
			Pid: uint32(pid),
		})
		if err != nil {
			return err
		}
		fmt.Printf("container deleted and returned exit status %d\n", r.ExitStatus)
		return nil
	},
}

var shimStateCommand = cli.Command{
	Name:  "state",
	Usage: "get the state of all the processes of the shim",
	Action: func(context *cli.Context) error {
		service, err := getShimService()
		if err != nil {
			return err
		}
		r, err := service.State(gocontext.Background(), &shim.StateRequest{})
		if err != nil {
			return err
		}
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf := bytes.NewBuffer(nil)
		if err := json.Indent(buf, data, " ", "    "); err != nil {
			return err
		}
		buf.WriteTo(os.Stdout)
		return nil
	},
}

var shimExecCommand = cli.Command{
	Name:  "exec",
	Usage: "exec a new process in the shim's container",
	Flags: append(fifoFlags,
		cli.BoolFlag{
			Name:  "attach,a",
			Usage: "stay attached to the container and open the fifos",
		},
		cli.StringSliceFlag{
			Name:  "env,e",
			Usage: "add environment vars",
			Value: &cli.StringSlice{},
		},
		cli.StringFlag{
			Name:  "cwd",
			Usage: "current working directory",
		},
	),
	Action: func(context *cli.Context) error {
		service, err := getShimService()
		if err != nil {
			return err
		}
		tty := context.Bool("tty")
		wg, err := prepareStdio(context.String("stdin"), context.String("stdout"), context.String("stderr"), tty)
		if err != nil {
			return err
		}

		// read spec file and extract Any object
		spec, err := ioutil.ReadFile(context.String("spec"))
		if err != nil {
			return err
		}

		rq := &shim.ExecRequest{
			Spec: &protobuf.Any{
				TypeUrl: specs.Version,
				Value:   spec,
			},
			Stdin:    context.String("stdin"),
			Stdout:   context.String("stdout"),
			Stderr:   context.String("stderr"),
			Terminal: tty,
		}
		r, err := service.Exec(gocontext.Background(), rq)
		if err != nil {
			return err
		}
		fmt.Printf("exec running with pid %d\n", r.Pid)
		if context.Bool("attach") {
			logrus.Info("attaching")
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
				if _, err := service.Pty(gocontext.Background(), &shim.PtyRequest{
					Pid:    r.Pid,
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

var shimEventsCommand = cli.Command{
	Name:  "events",
	Usage: "get events for a shim",
	Action: func(context *cli.Context) error {
		service, err := getShimService()
		if err != nil {
			return err
		}
		events, err := service.Events(gocontext.Background(), &shim.EventsRequest{})
		if err != nil {
			return err
		}
		for {
			e, err := events.Recv()
			if err != nil {
				return err
			}
			fmt.Printf("type=%s id=%s pid=%d status=%d\n", e.Type, e.ID, e.Pid, e.ExitStatus)
		}
	},
}

func getShimService() (shim.ShimClient, error) {
	bindSocket := "shim.sock"

	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithTimeout(100 * time.Second)}
	dialOpts = append(dialOpts,
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", bindSocket, timeout)
		},
		))
	conn, err := grpc.Dial(fmt.Sprintf("unix://%s", bindSocket), dialOpts...)
	if err != nil {
		return nil, err
	}
	return shim.NewShimClient(conn), nil
}
