// +build !windows

package shim

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/events"
	shim "github.com/containerd/containerd/linux/shim/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reaper"
	"github.com/containerd/containerd/sys"
	"google.golang.org/grpc"
)

type ClientOpt func(context.Context, Config) (shim.ShimClient, io.Closer, error)

// WithStart executes a new shim process
func WithStart(binary, address string, debug bool) ClientOpt {
	return func(ctx context.Context, config Config) (_ shim.ShimClient, _ io.Closer, err error) {
		socket, err := newSocket(config)
		if err != nil {
			return nil, nil, err
		}
		defer socket.Close()
		f, err := socket.File()
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to get fd for socket %s", config.Address)
		}
		defer f.Close()

		cmd := newCommand(binary, address, debug, config, f)
		if err := reaper.Default.Start(cmd); err != nil {
			return nil, nil, errors.Wrapf(err, "failed to start shim")
		}
		defer func() {
			if err != nil {
				terminate(cmd)
			}
		}()
		log.G(ctx).WithFields(logrus.Fields{
			"pid":     cmd.Process.Pid,
			"address": config.Address,
			"debug":   debug,
		}).Infof("shim %s started", binary)
		// set shim in cgroup if it is provided
		if config.CgroupPath != "" {
			if err := setCgroup(ctx, config, cmd); err != nil {
				return nil, nil, err
			}
		}
		if err = sys.SetOOMScore(cmd.Process.Pid, sys.OOMScoreMaxKillable); err != nil {
			return nil, nil, errors.Wrap(err, "failed to set OOM Score on shim")
		}
		c, clo, err := WithConnect(ctx, config)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to connect")
		}
		return c, clo, nil
	}
}

func terminate(cmd *exec.Cmd) {
	cmd.Process.Kill()
	reaper.Default.Wait(cmd)
}

func newCommand(binary, address string, debug bool, config Config, socket *os.File) *exec.Cmd {
	args := []string{
		"--namespace", config.Namespace,
		"--address", address,
	}
	if debug {
		args = append(args, "--debug")
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = config.Path
	// make sure the shim can be re-parented to system init
	// and is cloned in a new mount namespace because the overlay/filesystems
	// will be mounted by the shim
	cmd.SysProcAttr = &atter
	cmd.ExtraFiles = append(cmd.ExtraFiles, socket)
	if debug {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd
}

func newSocket(config Config) (*net.UnixListener, error) {
	if len(config.Address) > 106 {
		return nil, errors.Errorf("%q: unix socket path too long (limit 106)", config.Address)
	}
	l, err := net.Listen("unix", "\x00"+config.Address)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen to abstract unix socket %q", config.Address)
	}

	return l.(*net.UnixListener), nil
}

func connect(address string, d func(string, time.Duration) (net.Conn, error)) (*grpc.ClientConn, error) {
	gopts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithTimeout(100 * time.Second),
		grpc.WithDialer(d),
		grpc.FailOnNonTempDialError(true),
	}
	conn, err := grpc.Dial(dialAddress(address), gopts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return conn, nil
}

func dialer(address string, timeout time.Duration) (net.Conn, error) {
	address = strings.TrimPrefix(address, "unix://")
	return net.DialTimeout("unix", address, timeout)
}

func annonDialer(address string, timeout time.Duration) (net.Conn, error) {
	address = strings.TrimPrefix(address, "unix://")
	return net.DialTimeout("unix", "\x00"+address, timeout)
}

func dialAddress(address string) string {
	return fmt.Sprintf("unix://%s", address)
}

// WithConnect connects to an existing shim
func WithConnect(ctx context.Context, config Config) (shim.ShimClient, io.Closer, error) {
	conn, err := connect(config.Address, annonDialer)
	if err != nil {
		return nil, nil, err
	}
	return shim.NewShimClient(conn), conn, nil
}

// WithLocal uses an in process shim
func WithLocal(publisher events.Publisher) func(context.Context, Config) (shim.ShimClient, io.Closer, error) {
	return func(ctx context.Context, config Config) (shim.ShimClient, io.Closer, error) {
		service, err := NewService(config.Path, config.Namespace, publisher)
		if err != nil {
			return nil, nil, err
		}
		return NewLocal(service), nil, nil
	}
}

type Config struct {
	Address    string
	Path       string
	Namespace  string
	CgroupPath string
}

// New returns a new shim client
func New(ctx context.Context, config Config, opt ClientOpt) (*Client, error) {
	s, c, err := opt(ctx, config)
	if err != nil {
		return nil, err
	}
	return &Client{
		ShimClient: s,
		c:          c,
	}, nil
}

type Client struct {
	shim.ShimClient

	c io.Closer
}

func (c *Client) IsAlive(ctx context.Context) (bool, error) {
	_, err := c.ShimInfo(ctx, empty)
	if err != nil {
		if err != grpc.ErrServerStopped {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// KillShim kills the shim forcefully
func (c *Client) KillShim(ctx context.Context) error {
	info, err := c.ShimInfo(ctx, empty)
	if err != nil {
		return err
	}
	pid := int(info.ShimPid)
	// make sure we don't kill ourselves if we are running a local shim
	if os.Getpid() == pid {
		return nil
	}
	return unix.Kill(pid, unix.SIGKILL)
}

func (c *Client) Close() error {
	if c.c == nil {
		return nil
	}
	return c.c.Close()
}
