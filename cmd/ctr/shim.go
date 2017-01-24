package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os/exec"
	"syscall"
	"time"

	gocontext "context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"

	"github.com/docker/containerd/api/shim"
	"github.com/urfave/cli"
)

var shimCommand = cli.Command{
	Name:  "shim",
	Usage: "interact with a shim directly",
	Subcommands: []cli.Command{
		shimCreateCommand,
		shimStartCommand,
		shimDeleteCommand,
	},
	Action: func(context *cli.Context) error {
		cmd := exec.Command("containerd-shim", "--debug")
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		cmd.SysProcAttr.Setpgid = true
		if err := cmd.Start(); err != nil {
			return err
		}
		fmt.Println("new shim started @ ./shim.sock")
		return nil
	},
}

var shimCreateCommand = cli.Command{
	Name:  "create",
	Usage: "create a container with a shim",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "bundle",
			Usage: "bundle path for the container",
		},
		cli.StringFlag{
			Name:  "runtime",
			Value: "runc",
			Usage: "runtime to use for the container",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return fmt.Errorf("container id must be provided")
		}
		service, err := getShimService()
		if err != nil {
			return err
		}
		r, err := service.Create(gocontext.Background(), &shim.CreateRequest{
			ID:      id,
			Bundle:  context.String("bundle"),
			Runtime: context.String("runtime"),
		})
		if err != nil {
			return err
		}
		fmt.Printf("container created with id %s and pid %d\n", id, r.Pid)
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
		r, err := service.Delete(gocontext.Background(), &shim.DeleteRequest{})
		if err != nil {
			return err
		}
		fmt.Printf("container deleted and returned exit status %d", r.ExitStatus)
		return nil
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
