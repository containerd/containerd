package containerd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
)

// IO holds the io information for a task or process
type IO struct {
	// Terminal is true if one has been allocated
	Terminal bool
	// Stdin path
	Stdin string
	// Stdout path
	Stdout string
	// Stderr path
	Stderr string

	closer *wgCloser
}

// Cancel aborts all current io operations
func (i *IO) Cancel() {
	if i.closer == nil {
		return
	}
	i.closer.Cancel()
}

// Wait blocks until all io copy operations have completed
func (i *IO) Wait() {
	if i.closer == nil {
		return
	}
	i.closer.Wait()
}

// Close cleans up all open io resources
func (i *IO) Close() error {
	if i.closer == nil {
		return nil
	}
	return i.closer.Close()
}

// IOCreation creates new IO sets for a task
type IOCreation func(id string) (*IO, error)

// IOAttach allows callers to reattach to running tasks
type IOAttach func(*FIFOSet) (*IO, error)

// NewIO returns an IOCreation that will provide IO sets without a terminal
func NewIO(stdin io.Reader, stdout, stderr io.Writer) IOCreation {
	return NewIOWithTerminal(stdin, stdout, stderr, false)
}

// NewIOWithTerminal creates a new io set with the provied io.Reader/Writers for use with a terminal
func NewIOWithTerminal(stdin io.Reader, stdout, stderr io.Writer, terminal bool) IOCreation {
	return func(id string) (_ *IO, err error) {
		paths, err := NewFifos(id)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil && paths.Dir != "" {
				os.RemoveAll(paths.Dir)
			}
		}()
		i := &IO{
			Terminal: terminal,
			Stdout:   paths.Out,
			Stderr:   paths.Err,
			Stdin:    paths.In,
		}
		set := &ioSet{
			in:  stdin,
			out: stdout,
			err: stderr,
		}
		closer, err := copyIO(paths, set, i.Terminal)
		if err != nil {
			return nil, err
		}
		i.closer = closer
		return i, nil
	}
}

// WithAttach attaches the existing io for a task to the provided io.Reader/Writers
func WithAttach(stdin io.Reader, stdout, stderr io.Writer) IOAttach {
	return func(paths *FIFOSet) (*IO, error) {
		if paths == nil {
			return nil, fmt.Errorf("cannot attach to existing fifos")
		}
		i := &IO{
			Terminal: paths.Terminal,
			Stdout:   paths.Out,
			Stderr:   paths.Err,
			Stdin:    paths.In,
		}
		set := &ioSet{
			in:  stdin,
			out: stdout,
			err: stderr,
		}
		closer, err := copyIO(paths, set, i.Terminal)
		if err != nil {
			return nil, err
		}
		i.closer = closer
		return i, nil
	}
}

// Stdio returns an IO set to be used for a task
// that outputs the container's IO as the current processes Stdio
func Stdio(id string) (*IO, error) {
	return NewIO(os.Stdin, os.Stdout, os.Stderr)(id)
}

// StdioTerminal will setup the IO for the task to use a terminal
func StdioTerminal(id string) (*IO, error) {
	return NewIOWithTerminal(os.Stdin, os.Stdout, os.Stderr, true)(id)
}

// FIFOSet is a set of fifos for use with tasks
type FIFOSet struct {
	// Dir is the directory holding the task fifos
	Dir string
	// In, Out, and Err fifo paths
	In, Out, Err string
	// Terminal returns true if a terminal is being used for the task
	Terminal bool
}

type ioSet struct {
	in       io.Reader
	out, err io.Writer
}

type wgCloser struct {
	wg     *sync.WaitGroup
	dir    string
	set    []io.Closer
	cancel context.CancelFunc
}

func (g *wgCloser) Wait() {
	g.wg.Wait()
}

func (g *wgCloser) Close() error {
	for _, f := range g.set {
		f.Close()
	}
	if g.dir != "" {
		return os.RemoveAll(g.dir)
	}
	return nil
}

func (g *wgCloser) Cancel() {
	g.cancel()
}
