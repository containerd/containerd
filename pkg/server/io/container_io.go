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

package io

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/containerd/containerd"
	"github.com/golang/glog"

	cioutil "github.com/kubernetes-incubator/cri-containerd/pkg/ioutil"
	"github.com/kubernetes-incubator/cri-containerd/pkg/util"
)

// streamKey generates a key for the stream.
func streamKey(id, name string, stream StreamType) string {
	return strings.Join([]string{id, name, string(stream)}, "-")
}

// ContainerIO holds the container io.
type ContainerIO struct {
	id string

	fifos *containerd.FIFOSet
	*stdioPipes

	stdoutGroup *cioutil.WriterGroup
	stderrGroup *cioutil.WriterGroup

	closer *wgCloser
}

var _ containerd.IO = &ContainerIO{}

// ContainerIOOpts sets specific information to newly created ContainerIO.
type ContainerIOOpts func(*ContainerIO) error

// WithOutput adds output stream to the container io.
func WithOutput(name string, stdout, stderr io.WriteCloser) ContainerIOOpts {
	return func(c *ContainerIO) error {
		if stdout != nil {
			if err := c.stdoutGroup.Add(streamKey(c.id, name, Stdout), stdout); err != nil {
				return err
			}
		}
		if stderr != nil {
			if err := c.stderrGroup.Add(streamKey(c.id, name, Stderr), stderr); err != nil {
				return err
			}
		}
		return nil
	}
}

// WithFIFOs specifies existing fifos for the container io.
func WithFIFOs(fifos *containerd.FIFOSet) ContainerIOOpts {
	return func(c *ContainerIO) error {
		c.fifos = fifos
		return nil
	}
}

// WithNewFIFOs creates new fifos for the container io.
func WithNewFIFOs(root string, tty, stdin bool) ContainerIOOpts {
	return func(c *ContainerIO) error {
		fifos, err := newFifos(root, c.id, tty, stdin)
		if err != nil {
			return err
		}
		return WithFIFOs(fifos)(c)
	}
}

// NewContainerIO creates container io.
func NewContainerIO(id string, opts ...ContainerIOOpts) (_ *ContainerIO, err error) {
	c := &ContainerIO{
		id:          id,
		stdoutGroup: cioutil.NewWriterGroup(),
		stderrGroup: cioutil.NewWriterGroup(),
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.fifos == nil {
		return nil, errors.New("fifos are not set")
	}
	// Create actual fifos.
	stdio, closer, err := newStdioPipes(c.fifos)
	if err != nil {
		return nil, err
	}
	c.stdioPipes = stdio
	c.closer = closer
	return c, nil
}

// Config returns io config.
func (c *ContainerIO) Config() containerd.IOConfig {
	return containerd.IOConfig{
		Terminal: c.fifos.Terminal,
		Stdin:    c.fifos.In,
		Stdout:   c.fifos.Out,
		Stderr:   c.fifos.Err,
	}
}

// Pipe creates container fifos and pipe container output
// to output stream.
func (c *ContainerIO) Pipe() {
	wg := c.closer.wg
	wg.Add(1)
	go func() {
		if _, err := io.Copy(c.stdoutGroup, c.stdout); err != nil {
			glog.Errorf("Failed to pipe stdout of container %q: %v", c.id, err)
		}
		c.stdout.Close()
		c.stdoutGroup.Close()
		wg.Done()
		glog.V(2).Infof("Finish piping stdout of container %q", c.id)
	}()

	if !c.fifos.Terminal {
		wg.Add(1)
		go func() {
			if _, err := io.Copy(c.stderrGroup, c.stderr); err != nil {
				glog.Errorf("Failed to pipe stderr of container %q: %v", c.id, err)
			}
			c.stderr.Close()
			c.stderrGroup.Close()
			wg.Done()
			glog.V(2).Infof("Finish piping stderr of container %q", c.id)
		}()
	}
}

// Attach attaches container stdio.
// TODO(random-liu): Use pools.Copy in docker to reduce memory usage?
func (c *ContainerIO) Attach(opts AttachOptions) error {
	var wg sync.WaitGroup
	key := util.GenerateID()
	stdinKey := streamKey(c.id, "attach-"+key, Stdin)
	stdoutKey := streamKey(c.id, "attach-"+key, Stdout)
	stderrKey := streamKey(c.id, "attach-"+key, Stderr)

	var stdinStreamRC io.ReadCloser
	if c.stdin != nil && opts.Stdin != nil {
		// Create a wrapper of stdin which could be closed. Note that the
		// wrapper doesn't close the actual stdin, it only stops io.Copy.
		// The actual stdin will be closed by stream server.
		stdinStreamRC = cioutil.NewWrapReadCloser(opts.Stdin)
		wg.Add(1)
		go func() {
			if _, err := io.Copy(c.stdin, stdinStreamRC); err != nil {
				glog.Errorf("Failed to pipe stdin for container attach %q: %v", c.id, err)
			}
			glog.V(2).Infof("Attach stream %q closed", stdinKey)
			if opts.StdinOnce && !opts.Tty {
				// Due to kubectl requirements and current docker behavior, when (opts.StdinOnce &&
				// opts.Tty) we have to close container stdin and keep stdout and stderr open until
				// container stops.
				c.stdin.Close()
				// Also closes the containerd side.
				if err := opts.CloseStdin(); err != nil {
					glog.Errorf("Failed to close stdin for container %q: %v", c.id, err)
				}
			} else {
				if opts.Stdout != nil {
					c.stdoutGroup.Remove(stdoutKey)
				}
				if opts.Stderr != nil {
					c.stderrGroup.Remove(stderrKey)
				}
			}
			wg.Done()
		}()
	}

	attachStream := func(key string, close <-chan struct{}) {
		<-close
		glog.V(2).Infof("Attach stream %q closed", key)
		// Make sure stdin gets closed.
		if stdinStreamRC != nil {
			stdinStreamRC.Close()
		}
		wg.Done()
	}

	if opts.Stdout != nil {
		wg.Add(1)
		wc, close := cioutil.NewWriteCloseInformer(opts.Stdout)
		if err := c.stdoutGroup.Add(stdoutKey, wc); err != nil {
			return err
		}
		go attachStream(stdoutKey, close)
	}
	if !opts.Tty && opts.Stderr != nil {
		wg.Add(1)
		wc, close := cioutil.NewWriteCloseInformer(opts.Stderr)
		if err := c.stderrGroup.Add(stderrKey, wc); err != nil {
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
	c.closer.Close()
	if c.fifos != nil {
		return os.RemoveAll(c.fifos.Dir)
	}
	return nil
}
