// +build !windows

package tasks

import (
	gocontext "context"
	"os"
	"os/signal"

	"github.com/containerd/console"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// HandleConsoleResize resizes the console
func HandleConsoleResize(ctx gocontext.Context, task resizer, con console.Console) error {
	// do an initial resize of the console
	size, err := con.Size()
	if err != nil {
		return err
	}
	if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
		log.G(ctx).WithError(err).Error("resize pty")
	}
	s := make(chan os.Signal, 16)
	signal.Notify(s, unix.SIGWINCH)
	go func() {
		for range s {
			size, err := con.Size()
			if err != nil {
				log.G(ctx).WithError(err).Error("get pty size")
				continue
			}
			if err := task.Resize(ctx, uint32(size.Width), uint32(size.Height)); err != nil {
				log.G(ctx).WithError(err).Error("resize pty")
			}
		}
	}()
	return nil
}

// NewTask creates a new task
func NewTask(ctx gocontext.Context, client *containerd.Client, container containerd.Container, checkpoint string, tty, nullIO bool) (containerd.Task, error) {
	if checkpoint == "" {
		ioCreator := cio.Stdio
		if tty {
			ioCreator = cio.StdioTerminal
		}
		if nullIO {
			if tty {
				return nil, errors.New("tty and null-io cannot be used together")
			}
			ioCreator = cio.NullIO
		}
		return container.NewTask(ctx, ioCreator)
	}
	im, err := client.GetImage(ctx, checkpoint)
	if err != nil {
		return nil, err
	}
	return container.NewTask(ctx, cio.Stdio, containerd.WithTaskCheckpoint(im))
}
