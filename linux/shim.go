// +build linux

package linux

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"

	"github.com/containerd/containerd/api/services/shim"
	localShim "github.com/containerd/containerd/linux/shim"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/sys"
	"github.com/pkg/errors"
)

func newShim(shimName string, path string, remote bool) (shim.ShimClient, error) {
	if !remote {
		return localShim.Client(path)
	}
	socket := filepath.Join(path, "shim.sock")
	l, err := sys.CreateUnixSocket(socket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(shimName)
	cmd.Dir = path
	f, err := l.(*net.UnixListener).File()
	if err != nil {
		return nil, err
	}
	// close our side of the socket, do not close the listener as it will
	// remove the socket from disk
	defer f.Close()
	cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	// make sure the shim can be re-parented to system init
	// and is cloned in a new mount namespace because the overlay/filesystems
	// will be mounted by the shim
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS,
		Setpgid:    true,
	}
	if err := reaper.Default.Start(cmd); err != nil {
		return nil, errors.Wrapf(err, "failed to start shim")
	}
	if err := sys.SetOOMScore(cmd.Process.Pid, sys.OOMScoreMaxKillable); err != nil {
		return nil, err
	}
	return connectShim(socket)
}

func loadShim(path string, remote bool) (shim.ShimClient, error) {
	if !remote {
		return localShim.Client(path)
	}
	socket := filepath.Join(path, "shim.sock")
	return connectShim(socket)
}

func connectShim(socket string) (shim.ShimClient, error) {
	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
	grpclog.SetLogger(log.New(ioutil.Discard, "", log.LstdFlags))
	dialOpts := []grpc.DialOption{grpc.WithInsecure(), grpc.WithTimeout(100 * time.Second)}
	dialOpts = append(dialOpts,
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", socket, timeout)
		}),
		grpc.WithBlock(),
		grpc.WithTimeout(2*time.Second),
	)
	conn, err := grpc.Dial(fmt.Sprintf("unix://%s", socket), dialOpts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to connect to shim via \"%s\"", fmt.Sprintf("unix://%s", socket))
	}
	return shim.NewShimClient(conn), nil
}
