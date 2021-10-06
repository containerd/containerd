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
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"

	. "github.com/containerd/containerd"

	exec "golang.org/x/sys/execabs"
)

type daemon struct {
	sync.Mutex
	addr string
	cmd  *exec.Cmd
}

func (d *daemon) start(name, address string, args []string, stdout, stderr io.Writer) error {
	d.Lock()
	defer d.Unlock()
	if d.cmd != nil {
		return errors.New("daemon is already running")
	}
	args = append(args, []string{"--address", address}...)
	cmd := exec.Command(name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cmd.Wait()
		return fmt.Errorf("failed to start daemon: %w", err)
	}
	d.addr = address
	d.cmd = cmd
	return nil
}

func (d *daemon) waitForStart(ctx context.Context) (*Client, error) {
	var (
		client  *Client
		serving bool
		err     error
	)

	client, err = New(d.addr)
	if err != nil {
		return nil, err
	}
	serving, err = client.IsServing(ctx)
	if !serving {
		client.Close()
		if err == nil {
			err = errors.New("connection was successful but service is not available")
		}
		return nil, err
	}
	return client, err
}

func (d *daemon) Stop() error {
	d.Lock()
	defer d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}
	return d.cmd.Process.Signal(syscall.SIGTERM)
}

func (d *daemon) Kill() error {
	d.Lock()
	defer d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}
	return d.cmd.Process.Kill()
}

func (d *daemon) Wait() error {
	d.Lock()
	defer d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}
	err := d.cmd.Wait()
	d.cmd = nil
	return err
}

func (d *daemon) Restart(stopCb func()) error {
	d.Lock()
	defer d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}

	var err error
	if err = d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal daemon: %w", err)
	}

	d.cmd.Wait()

	if stopCb != nil {
		stopCb()
	}

	cmd := exec.Command(d.cmd.Path, d.cmd.Args[1:]...)
	cmd.Stdout = d.cmd.Stdout
	cmd.Stderr = d.cmd.Stderr
	if err := cmd.Start(); err != nil {
		cmd.Wait()
		return fmt.Errorf("failed to start new daemon instance: %w", err)
	}
	d.cmd = cmd

	return nil
}
