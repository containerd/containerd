// +build !windows

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

package client

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/runtime/shim"
	shimapi "github.com/containerd/containerd/runtime/shim/v1"
	"github.com/containerd/containerd/sys"
)

// WithStart executes a new shim process
func WithStart(binary, address, daemonAddress, cgroup string, debug bool, exitHandler func()) Opt {
	return func(ctx context.Context, config shim.Config) (_ shimapi.ShimService, _ io.Closer, err error) {
		socket, err := newSocket(address)
		if err != nil {
			return nil, nil, err
		}
		defer socket.Close()
		f, err := socket.File()
		if err != nil {
			return nil, nil, errors.Wrapf(err, "failed to get fd for socket %s", address)
		}
		defer f.Close()

		cmd, err := newCommand(binary, daemonAddress, debug, config, f)
		if err != nil {
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, errors.Wrapf(err, "failed to start shim")
		}
		defer func() {
			if err != nil {
				cmd.Process.Kill()
			}
		}()
		go func() {
			cmd.Wait()
			exitHandler()
		}()
		log.G(ctx).WithFields(logrus.Fields{
			"pid":     cmd.Process.Pid,
			"address": address,
			"debug":   debug,
		}).Infof("shim %s started", binary)
		// set shim in cgroup if it is provided
		if cgroup != "" {
			if err := setCgroup(cgroup, cmd); err != nil {
				return nil, nil, err
			}
			log.G(ctx).WithFields(logrus.Fields{
				"pid":     cmd.Process.Pid,
				"address": address,
			}).Infof("shim placed in cgroup %s", cgroup)
		}
		if err = sys.SetOOMScore(cmd.Process.Pid, sys.OOMScoreMaxKillable); err != nil {
			return nil, nil, errors.Wrap(err, "failed to set OOM Score on shim")
		}
		c, clo, err := WithConnect(address, func() {})(ctx, config)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to connect")
		}
		return c, clo, nil
	}
}

func newCommand(binary, daemonAddress string, debug bool, config shim.Config, socket *os.File) (*exec.Cmd, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-namespace", config.Namespace,
		"-workdir", config.WorkDir,
		"-address", daemonAddress,
		"-containerd-binary", selfExe,
	}

	if config.Criu != "" {
		args = append(args, "-criu-path", config.Criu)
	}
	if config.RuntimeRoot != "" {
		args = append(args, "-runtime-root", config.RuntimeRoot)
	}
	if config.SystemdCgroup {
		args = append(args, "-systemd-cgroup")
	}
	if debug {
		args = append(args, "-debug")
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = config.Path
	// make sure the shim can be re-parented to system init
	// and is cloned in a new mount namespace because the overlay/filesystems
	// will be mounted by the shim
	cmd.SysProcAttr = getSysProcAttr()
	cmd.ExtraFiles = append(cmd.ExtraFiles, socket)
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	if debug {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd, nil
}

func newSocket(address string) (*net.UnixListener, error) {
	if len(address) > 106 {
		return nil, errors.Errorf("%q: unix socket path too long (> 106)", address)
	}
	l, err := net.Listen("unix", "\x00"+address)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen to abstract unix socket %q", address)
	}

	return l.(*net.UnixListener), nil
}

func annonDialer(address string, timeout time.Duration) (net.Conn, error) {
	address = strings.TrimPrefix(address, "unix://")
	return net.DialTimeout("unix", "\x00"+address, timeout)
}

// StopShim signals the shim to exit and wait for the process to disappear
func (c *Client) StopShim(ctx context.Context) error {
	return c.signalShim(ctx, unix.SIGTERM)
}

// KillShim kills the shim forcefully and wait for the process to disappear
func (c *Client) KillShim(ctx context.Context) error {
	return c.signalShim(ctx, unix.SIGKILL)
}

func (c *Client) signalShim(ctx context.Context, sig syscall.Signal) error {
	info, err := c.ShimInfo(ctx, empty)
	if err != nil {
		return err
	}
	pid := int(info.ShimPid)
	// make sure we don't kill ourselves if we are running a local shim
	if os.Getpid() == pid {
		return nil
	}
	if err := unix.Kill(pid, sig); err != nil && err != unix.ESRCH {
		return err
	}
	// wait for shim to die after being signaled
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.waitForExit(pid):
		return nil
	}
}

func (c *Client) waitForExit(pid int) <-chan struct{} {
	c.exitOnce.Do(func() {
		for {
			// use kill(pid, 0) here because the shim could have been reparented
			// and we are no longer able to waitpid(pid, ...) on the shim
			if err := unix.Kill(pid, 0); err == unix.ESRCH {
				close(c.exitCh)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return c.exitCh
}
