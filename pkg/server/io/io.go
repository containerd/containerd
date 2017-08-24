/*
Copyright 2017 The Kubernetes Authors.

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

package agents

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd"
	"github.com/containerd/fifo"
	"github.com/golang/glog"
	"golang.org/x/net/context"

	"github.com/kubernetes-incubator/cri-containerd/pkg/ioutil"
	"github.com/kubernetes-incubator/cri-containerd/pkg/util"
)

// StreamType is the type of the stream, stdout/stderr.
type StreamType string

const (
	// Stdin stream type.
	Stdin StreamType = "stdin"
	// Stdout stream type.
	Stdout StreamType = "stdout"
	// Stderr stream type.
	Stderr StreamType = "stderr"
)

type wgCloser struct {
	ctx    context.Context
	wg     *sync.WaitGroup
	set    []io.Closer
	cancel context.CancelFunc
}

func (g *wgCloser) Wait() {
	g.wg.Wait()
}

func (g *wgCloser) Close() {
	for _, f := range g.set {
		f.Close()
	}
}

func (g *wgCloser) Cancel() {
	g.cancel()
}

// streamKey generates a key for the stream.
func streamKey(id, name string, stream StreamType) string {
	return strings.Join([]string{id, name, string(stream)}, "-")
}

// ContainerIO holds the container io.
type ContainerIO struct {
	dir        string
	stdinPath  string
	stdoutPath string
	stderrPath string

	id     string
	tty    bool
	stdin  bool
	stdout *ioutil.WriterGroup
	stderr *ioutil.WriterGroup

	closer *wgCloser
}

var _ containerd.IO = &ContainerIO{}

// Opts sets specific information to newly created ContainerIO.
type Opts func(*ContainerIO) error

// WithStdin enables stdin of the container io.
func WithStdin(stdin bool) Opts {
	return func(c *ContainerIO) error {
		c.stdin = stdin
		return nil
	}
}

// WithOutput adds output stream to the container io.
func WithOutput(name string, stdout, stderr io.WriteCloser) Opts {
	return func(c *ContainerIO) error {
		if stdout != nil {
			if err := c.stdout.Add(streamKey(c.id, name, Stdout), stdout); err != nil {
				return err
			}
		}
		if stderr != nil {
			if err := c.stderr.Add(streamKey(c.id, name, Stderr), stderr); err != nil {
				return err
			}
		}
		return nil
	}
}

// WithTerminal enables tty of the container io.
func WithTerminal(tty bool) Opts {
	return func(c *ContainerIO) error {
		c.tty = tty
		return nil
	}
}

// NewContainerIO creates container io.
func NewContainerIO(id string, opts ...Opts) (*ContainerIO, error) {
	fifos, err := containerd.NewFifos(id)
	if err != nil {
		return nil, err
	}
	c := &ContainerIO{
		id:         id,
		dir:        fifos.Dir,
		stdoutPath: fifos.Out,
		stderrPath: fifos.Err,
		stdout:     ioutil.NewWriterGroup(),
		stderr:     ioutil.NewWriterGroup(),
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.stdin {
		c.stdinPath = fifos.In
	}
	return c, nil
}

// Config returns io config.
func (c *ContainerIO) Config() containerd.IOConfig {
	return containerd.IOConfig{
		Terminal: c.tty,
		Stdin:    c.stdinPath,
		Stdout:   c.stdoutPath,
		Stderr:   c.stderrPath,
	}
}

// Pipe creates container fifos and pipe container output
// to output stream.
func (c *ContainerIO) Pipe() (err error) {
	var (
		f           io.ReadWriteCloser
		set         []io.Closer
		ctx, cancel = context.WithCancel(context.Background())
		wg          = &sync.WaitGroup{}
	)
	defer func() {
		if err != nil {
			for _, f := range set {
				f.Close()
			}
			cancel()
		}
	}()
	if c.stdinPath != "" {
		// Just create the stdin, only open it when used.
		if f, err = fifo.OpenFifo(ctx, c.stdinPath, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
			return err
		}
		f.Close()
	}

	if f, err = fifo.OpenFifo(ctx, c.stdoutPath, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return err
	}
	set = append(set, f)
	wg.Add(1)
	go func(r io.ReadCloser) {
		if _, err := io.Copy(c.stdout, r); err != nil {
			glog.Errorf("Failed to redirect stdout of container %q: %v", c.id, err)
		}
		r.Close()
		c.stdout.Close()
		wg.Done()
		glog.V(2).Infof("Finish piping stdout of container %q", c.id)
	}(f)

	if f, err = fifo.OpenFifo(ctx, c.stderrPath, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return err
	}
	set = append(set, f)
	if !c.tty {
		wg.Add(1)
		go func(r io.ReadCloser) {
			if _, err := io.Copy(c.stderr, r); err != nil {
				glog.Errorf("Failed to redirect stderr of container %q: %v", c.id, err)
			}
			r.Close()
			c.stderr.Close()
			wg.Done()
			glog.V(2).Infof("Finish piping stderr of container %q", c.id)
		}(f)
	}
	c.closer = &wgCloser{
		wg:     wg,
		set:    set,
		ctx:    ctx,
		cancel: cancel,
	}
	return nil
}

// Attach attaches container stdio.
func (c *ContainerIO) Attach(stdin io.Reader, stdout, stderr io.WriteCloser) error {
	if c.closer == nil {
		return errors.New("container io is not initialized")
	}
	var wg sync.WaitGroup
	key := util.GenerateID()
	stdinKey := streamKey(c.id, "attach-"+key, Stdin)
	stdoutKey := streamKey(c.id, "attach-"+key, Stdout)
	stderrKey := streamKey(c.id, "attach-"+key, Stderr)

	var stdinCloser io.Closer
	if c.stdinPath != "" && stdin != nil {
		f, err := fifo.OpenFifo(c.closer.ctx, c.stdinPath, syscall.O_WRONLY|syscall.O_NONBLOCK, 0700)
		if err != nil {
			return err
		}
		// Also increase wait group here, so that `closer.Wait` will
		// also wait for this fifo to be closed.
		c.closer.wg.Add(1)
		wg.Add(1)
		go func(w io.WriteCloser) {
			if _, err := io.Copy(w, stdin); err != nil {
				glog.Errorf("Failed to redirect stdin for container attach %q: %v", c.id, err)
			}
			w.Close()
			glog.V(2).Infof("Attach stream %q closed", stdinKey)
			// No matter what, when stdin is closed (io.Copy unblock), close stdout and stderr
			if stdout != nil {
				c.stdout.Remove(stdoutKey)
			}
			if stderr != nil {
				c.stderr.Remove(stderrKey)
			}
			wg.Done()
			c.closer.wg.Done()
		}(f)
		stdinCloser = f
	}

	attachStream := func(key string, close <-chan struct{}) {
		<-close
		glog.V(2).Infof("Attach stream %q closed", key)
		// Make sure stdin gets closed.
		if stdinCloser != nil {
			stdinCloser.Close()
		}
		wg.Done()
	}

	if stdout != nil {
		wg.Add(1)
		wc, close := ioutil.NewWriteCloseInformer(stdout)
		if err := c.stdout.Add(stdoutKey, wc); err != nil {
			return err
		}
		go attachStream(stdoutKey, close)
	}
	if !c.tty && stderr != nil {
		wg.Add(1)
		wc, close := ioutil.NewWriteCloseInformer(stderr)
		if err := c.stderr.Add(stderrKey, wc); err != nil {
			return err
		}
		go attachStream(stderrKey, close)
	}
	wg.Wait()
	return nil
}

// Cancel cancels container io.
func (c *ContainerIO) Cancel() {
	c.closer.Cancel()
}

// Wait waits container io to finish.
func (c *ContainerIO) Wait() {
	c.closer.Wait()
}

// Close closes all FIFOs.
func (c *ContainerIO) Close() error {
	if c.closer != nil {
		c.closer.Close()
	}
	if c.dir != "" {
		return os.RemoveAll(c.dir)
	}
	return nil
}
