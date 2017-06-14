// +build linux

package linux

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/containerd/containerd/api/services/shim"
	localShim "github.com/containerd/containerd/linux/shim"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/sys"
	"github.com/pkg/errors"
)

func newShim(ctx context.Context, shimName string, path, namespace string, remote bool) (shim.ShimClient, error) {
	if !remote {
		return localShim.Client(path, namespace)
	}
	socket := filepath.Join(path, "shim.sock")
	l, err := sys.CreateUnixSocket(socket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(shimName, "--namespace", namespace)
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
	defer func() {
		if err != nil {
			cmd.Process.Kill()
			reaper.Default.Wait(cmd)
		} else {
			log.G(ctx).WithField("socket", socket).Infof("new shim started")
		}
	}()
	if err = sys.SetOOMScore(cmd.Process.Pid, sys.OOMScoreMaxKillable); err != nil {
		return nil, errors.Wrap(err, "failed to set OOM Score on shim")
	}
	return connectShim(socket)
}

func loadShim(path, namespace string, remote bool) (shim.ShimClient, error) {
	if !remote {
		return localShim.Client(path, namespace)
	}
	socket := filepath.Join(path, "shim.sock")
	return connectShim(socket)
}

func connectShim(socket string) (shim.ShimClient, error) {
	// reset the logger for grpc to log to dev/null so that it does not mess with our stdio
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
