// +build !solaris

package main

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/containerd/containerd/osutils"
	"github.com/containerd/console"
	runc "github.com/containerd/go-runc"
	"github.com/tonistiigi/fifo"
	"golang.org/x/net/context"
)

// openIO opens the pre-created fifo's for use with the container
// in RDWR so that they remain open if the other side stops listening
func (p *process) openIO() error {
	p.stdio = &stdio{}
	var (
		uid = p.state.RootUID
		gid = p.state.RootGID
	)

	ctx, _ := context.WithTimeout(context.Background(), 15*time.Second)

	stdinCloser, err := fifo.OpenFifo(ctx, p.state.Stdin, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	p.stdinCloser = stdinCloser

	if p.state.Terminal {
		socket, err := runc.NewTempConsoleSocket()
		if err != nil {
			return err
		}
		p.socket = socket
		consoleCh := p.waitConsole(socket)

		stdin, err := fifo.OpenFifo(ctx, p.state.Stdin, syscall.O_RDONLY, 0)
		if err != nil {
			return err
		}
		stdoutw, err := fifo.OpenFifo(ctx, p.state.Stdout, syscall.O_WRONLY, 0)
		if err != nil {
			return err
		}
		stdoutr, err := fifo.OpenFifo(ctx, p.state.Stdout, syscall.O_RDONLY, 0)
		if err != nil {
			return err
		}
		// open the fifos but wait until we receive the console before we start
		// copying data back and forth between the two
		go p.setConsole(consoleCh, stdin, stdoutw)

		p.Add(1)
		p.ioCleanupFn = func() {
			stdoutr.Close()
			stdoutw.Close()
		}
		return nil
	}
	close(p.consoleErrCh)
	i, err := p.initializeIO(uid, gid)
	if err != nil {
		return err
	}
	p.shimIO = i
	// non-tty
	ioClosers := []io.Closer{}
	for _, pair := range []struct {
		name string
		dest func(wc io.WriteCloser, rc io.Closer)
	}{
		{
			p.state.Stdout,
			func(wc io.WriteCloser, rc io.Closer) {
				p.Add(1)
				go func() {
					io.Copy(wc, i.Stdout)
					p.Done()
				}()
			},
		},
		{
			p.state.Stderr,
			func(wc io.WriteCloser, rc io.Closer) {
				p.Add(1)
				go func() {
					io.Copy(wc, i.Stderr)
					p.Done()
				}()
			},
		},
	} {
		fw, err := fifo.OpenFifo(ctx, pair.name, syscall.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("containerd-shim: opening %s failed: %s", pair.name, err)
		}
		fr, err := fifo.OpenFifo(ctx, pair.name, syscall.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("containerd-shim: opening %s failed: %s", pair.name, err)
		}
		pair.dest(fw, fr)
		ioClosers = append(ioClosers, fw, fr)
	}

	f, err := fifo.OpenFifo(ctx, p.state.Stdin, syscall.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("containerd-shim: opening %s failed: %s", p.state.Stdin, err)
	}
	p.ioCleanupFn = func() {
		for _, c := range ioClosers {
			c.Close()
		}
	}
	go func() {
		io.Copy(i.Stdin, f)
		i.Stdin.Close()
		f.Close()
	}()

	return nil
}

func (p *process) Wait() {
	p.WaitGroup.Wait()
	if p.ioCleanupFn != nil {
		p.ioCleanupFn()
	}
	if p.console != nil {
		p.console.Close()
	}
}

func (p *process) killAll() error {
	if !p.state.Exec {
		cmd := exec.Command(p.runtime, append(p.state.RuntimeArgs, "kill", "--all", p.id, "SIGKILL")...)
		cmd.SysProcAttr = osutils.SetPDeathSig()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %v", out, err)
		}
	}
	return nil
}

func (p *process) setConsole(c <-chan *consoleR, stdin io.Reader, stdout io.Writer) {
	r := <-c
	if r.err != nil {
		p.consoleErrCh <- r.err
		return
	}
	close(p.consoleErrCh)
	p.console = r.c
	// copy from the console into the provided fifos
	go io.Copy(r.c, stdin)
	go func() {
		io.Copy(stdout, r.c)
		p.Done()
	}()
}

type consoleR struct {
	c   console.Console
	err error
}

func (p *process) waitConsole(socket *runc.Socket) <-chan *consoleR {
	c := make(chan *consoleR, 1)
	go func() {
		master, err := socket.ReceiveMaster()
		socket.Close()
		if err != nil {
			c <- &consoleR{
				err: err,
			}
			return
		}
		c <- &consoleR{
			c: master,
		}
	}()
	return c

}
