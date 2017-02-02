package supervisor

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/docker/containerd/api/shim"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
)

func newShimClient(root string) (shim.ShimClient, error) {
	// TODO: start the shim process
	cmd := exec.Command("containerd-shim")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cmd.Dir = root

	socket := filepath.Join(root, "shim.sock")
	return connectToShim(socket)
}

func connectToShim(socket string) (shim.ShimClient, error) {
	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithTimeout(100 * time.Second)}
	dialOpts = append(dialOpts,
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", socket, timeout)
		},
		))
	// FIXME: probably need a retry here
	conn, err := grpc.Dial(fmt.Sprintf("unix://%s", socket), dialOpts...)
	if err != nil {
		return nil, err
	}
	return shim.NewShimClient(conn), nil
}
