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

	"github.com/docker/containerd/api/services/shim"
	localShim "github.com/docker/containerd/linux/shim"
	"github.com/docker/containerd/reaper"
	"github.com/docker/containerd/sys"
	"github.com/pkg/errors"
)

func newShim(path string, remote bool) (shim.ShimClient, error) {
	if !remote {
		return localShim.Client(path), nil
	}
	socket := filepath.Join(path, "shim.sock")
	l, err := sys.CreateUnixSocket(socket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("containerd-shim")
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
	return connectShim(socket)
}

func loadShim(path string, remote bool) (shim.ShimClient, error) {
	if !remote {
		return localShim.Client(path), nil
	}
	socket := filepath.Join(path, "shim.sock")
	return connectShim(socket)
	// TODO: failed to connect to the shim, check if it's alive
	//   - if it is kill it
	//   - in both case call runc killall and runc delete on the id
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
