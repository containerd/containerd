package containerd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
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
		return errors.Wrap(err, "failed to start daemon")
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

	for i := 0; i < 20; i++ {
		if client == nil {
			client, err = New(d.addr)
		}
		if err == nil {
			serving, err = client.IsServing(ctx)
			if serving {
				return client, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("containerd did not start within 2s: %v", err)
}

func (d *daemon) Stop() error {
	d.Lock()
	d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}
	return d.cmd.Process.Signal(syscall.SIGTERM)
}

func (d *daemon) Kill() error {
	d.Lock()
	d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}
	return d.cmd.Process.Kill()
}

func (d *daemon) Wait() error {
	d.Lock()
	d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}
	return d.cmd.Wait()
}

func (d *daemon) Restart() error {
	d.Lock()
	d.Unlock()
	if d.cmd == nil {
		return errors.New("daemon is not running")
	}

	var err error
	if err = d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return errors.Wrap(err, "failed to signal daemon")
	}

	d.cmd.Wait()

	<-time.After(1 * time.Second)

	cmd := exec.Command(d.cmd.Path, d.cmd.Args[1:]...)
	cmd.Stdout = d.cmd.Stdout
	cmd.Stderr = d.cmd.Stderr
	if err := cmd.Start(); err != nil {
		cmd.Wait()
		return errors.Wrap(err, "failed to start new daemon instance")
	}
	d.cmd = cmd

	return nil
}
