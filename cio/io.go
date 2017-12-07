package cio

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
)

// Config holds the io configurations.
type Config struct {
	// Terminal is true if one has been allocated
	Terminal bool
	// Stdin path
	Stdin string
	// Stdout path
	Stdout string
	// Stderr path
	Stderr string
}

// IO holds the io information for a task or process
type IO interface {
	// Config returns the IO configuration.
	Config() Config
	// Cancel aborts all current io operations.
	Cancel()
	// Wait blocks until all io copy operations have completed.
	Wait()
	// Close cleans up all open io resources. Cancel() is always called before
	// Close()
	Close() error
}

// Creator creates new IO sets for a task
type Creator func(id string) (IO, error)

// Attach allows callers to reattach to running tasks
//
// There should only be one reader for a task's IO set
// because fifo's can only be read from one reader or the output
// will be sent only to the first reads
type Attach func(*FIFOSet) (IO, error)

// FIFOSet is a set of file paths to FIFOs for a task's standard IO streams
type FIFOSet struct {
	Config
	close func() error
}

func (f *FIFOSet) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

// NewFIFOSet returns a new FIFOSet from a Config and a close function
func NewFIFOSet(config Config, close func() error) *FIFOSet {
	return &FIFOSet{Config: config, close: close}
}

// NewIO returns an Creator that will provide IO sets without a terminal
func NewIO(stdin io.Reader, stdout, stderr io.Writer) Creator {
	return NewIOWithTerminal(stdin, stdout, stderr, false)
}

// NewIOWithTerminal creates a new io set with the provided io.Reader/Writers for use with a terminal
func NewIOWithTerminal(stdin io.Reader, stdout, stderr io.Writer, terminal bool) Creator {
	return func(id string) (IO, error) {
		fifos, err := newFIFOSetInTempDir(id)
		if err != nil {
			return nil, err
		}

		fifos.Terminal = terminal
		set := &ioSet{in: stdin, out: stdout, err: stderr}
		return copyIO(fifos, set)
	}
}

// WithAttach attaches the existing io for a task to the provided io.Reader/Writers
func WithAttach(stdin io.Reader, stdout, stderr io.Writer) Attach {
	return func(fifos *FIFOSet) (IO, error) {
		if fifos == nil {
			return nil, fmt.Errorf("cannot attach, missing fifos")
		}
		set := &ioSet{in: stdin, out: stdout, err: stderr}
		return copyIO(fifos, set)
	}
}

// Stdio returns an IO set to be used for a task
// that outputs the container's IO as the current processes Stdio
func Stdio(id string) (IO, error) {
	return NewIO(os.Stdin, os.Stdout, os.Stderr)(id)
}

// StdioTerminal will setup the IO for the task to use a terminal
func StdioTerminal(id string) (IO, error) {
	return NewIOWithTerminal(os.Stdin, os.Stdout, os.Stderr, true)(id)
}

// NullIO redirects the container's IO into /dev/null
func NullIO(_ string) (IO, error) {
	return &cio{}, nil
}

type ioSet struct {
	in       io.Reader
	out, err io.Writer
}

type pipes struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser
}

func (p *pipes) closers() []io.Closer {
	return []io.Closer{p.Stdin, p.Stdout, p.Stderr}
}

// cio is a basic container IO implementation.
type cio struct {
	config  Config
	wg      *sync.WaitGroup
	closers []io.Closer
	cancel  context.CancelFunc
}

func (c *cio) Config() Config {
	return c.config
}

func (c *cio) Wait() {
	if c.wg != nil {
		c.wg.Wait()
	}
}

func (c *cio) Close() error {
	var lastErr error
	for _, closer := range c.closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (c *cio) Cancel() {
	if c.cancel != nil {
		c.cancel()
	}
}
