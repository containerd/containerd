// +build windows

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
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/runtime/shim"
	shimapi "github.com/containerd/containerd/runtime/shim/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// WithStart executes a new shim process
func WithStart(binary, address, daemonAddress string, debug bool, exitHandler func()) Opt {
	return func(ctx context.Context, config shim.Config) (_ shimapi.ShimService, _ io.Closer, err error) {
		cmd, err := newCommand(binary, address, daemonAddress, debug, config)
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
		c, clo, err := WithConnect(address, func() {})(ctx, config)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to connect")
		}
		return c, clo, nil
	}
}

func newCommand(binary, socketAddress, daemonAddress string, debug bool, config shim.Config) (*exec.Cmd, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-namespace", config.Namespace,
		"-socket", socketAddress,
		"-address", daemonAddress,
		"-containerd-binary", selfExe,
	}

	if config.RuntimeRoot != "" {
		args = append(args, "-runtime-root", config.RuntimeRoot)
	}
	if debug {
		args = append(args, "-debug")
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = config.Path
	cmd.Env = append(os.Environ(), "GOMAXPROCS=2")
	if debug {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd, nil
}

func annonDialer(address string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(address, &timeout)
}

// StopShim signals the shim to exit and wait for the process to disappear
func (c *Client) StopShim(ctx context.Context) error {
	// TODO: JTERRY75
	return errors.New("not implemented")
}

// KillShim kills the shim forcefully and wait for the process to disappear
func (c *Client) KillShim(ctx context.Context) error {
	// TODO: JTERRY75
	return errors.New("not implemented")
}
