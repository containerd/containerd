package linux

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"

	"github.com/docker/containerd/api/services/shim"
	"github.com/docker/containerd/utils"
	"github.com/pkg/errors"
)

func newShim(path string, extraFiles []*os.File) (shim.ShimClient, error) {
	socket := filepath.Join(path, "shim.sock")
	l, err := utils.CreateUnixSocket(socket)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("containerd-shim")
	cmd.Dir = path
	cmd.ExtraFiles = append(cmd.ExtraFiles, extraFiles...)
	cmd.Env = os.Environ()
	f, err := l.(*net.UnixListener).File()
	if err != nil {
		return nil, err
	}
	// close our side of the socket, do not close the listener as it will
	// remove the socket from disk
	defer f.Close()
	cmd.Env = append(cmd.Env, fmt.Sprintf("CONTAINERD_SHIM_GRPC_FD=%d", 3+len(cmd.ExtraFiles)))
	cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	// make sure the shim can be re-parented to system init
	// and is cloned in a new mount namespace because the overlay/filesystems
	// will be mounted by the shim
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS,
		Setpgid:    true,
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Wrapf(err, "failed to start shim")
	}

	// Close the extra files, they've been inhereted by the child now
	for _, f := range cmd.ExtraFiles {
		f.Close()
	}

	// since we are currently the parent go ahead and make sure we wait on the shim
	go cmd.Wait()
	return connectShim(socket)
}

func loadShim(path string) (shim.ShimClient, error) {
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
