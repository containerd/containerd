package containerd

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

type IO struct {
	Terminal bool
	Stdin    string
	Stdout   string
	Stderr   string

	closer io.Closer
}

func (i *IO) Close() error {
	if i.closer == nil {
		return nil
	}
	return i.closer.Close()
}

type IOCreation func() (*IO, error)

func NewIO(stdin io.Reader, stdout, stderr io.Writer) IOCreation {
	return func() (*IO, error) {
		paths, err := NewFifos()
		if err != nil {
			return nil, err
		}
		i := &IO{
			Terminal: false,
			Stdout:   paths.Out,
			Stderr:   paths.Err,
			Stdin:    paths.In,
		}
		set := &ioSet{
			in:  stdin,
			out: stdout,
			err: stderr,
		}
		closer, err := copyIO(paths, set, false)
		if err != nil {
			return nil, err
		}
		i.closer = closer
		return i, nil
	}
}

func WithIO(stdin io.Reader, stdout, stderr io.Writer, dir string) IOCreation {
	return func() (*IO, error) {
		paths, err := WithFifos(dir)
		if err != nil {
			return nil, err
		}
		i := &IO{
			Terminal: false,
			Stdout:   paths.Out,
			Stderr:   paths.Err,
			Stdin:    paths.In,
		}
		set := &ioSet{
			in:  stdin,
			out: stdout,
			err: stderr,
		}
		closer, err := copyIO(paths, set, false)
		if err != nil {
			return nil, err
		}
		i.closer = closer
		return i, nil
	}
}

// Stdio returns an IO implementation to be used for a task
// that outputs the container's IO as the current processes Stdio
func Stdio() (*IO, error) {
	paths, err := NewFifos()
	if err != nil {
		return nil, err
	}
	set := &ioSet{
		in:  os.Stdin,
		out: os.Stdout,
		err: os.Stderr,
	}
	closer, err := copyIO(paths, set, false)
	if err != nil {
		return nil, err
	}
	return &IO{
		Terminal: false,
		Stdin:    paths.In,
		Stdout:   paths.Out,
		Stderr:   paths.Err,
		closer:   closer,
	}, nil
}

// NewFifos returns a new set of fifos for the task
func NewFifos() (*FifoSet, error) {
	root := filepath.Join(os.TempDir(), "containerd")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := ioutil.TempDir(root, "")
	if err != nil {
		return nil, err
	}
	return &FifoSet{
		Dir: dir,
		In:  filepath.Join(dir, "stdin"),
		Out: filepath.Join(dir, "stdout"),
		Err: filepath.Join(dir, "stderr"),
	}, nil
}

// WithFifos returns existing or creates new fifos inside an existing dir
func WithFifos(dir string) (*FifoSet, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &FifoSet{
		Dir: dir,
		In:  filepath.Join(dir, "stdin"),
		Out: filepath.Join(dir, "stdout"),
		Err: filepath.Join(dir, "stderr"),
	}, nil
}

type FifoSet struct {
	// Dir is the directory holding the task fifos
	Dir          string
	In, Out, Err string
}

type ioSet struct {
	in       io.Reader
	out, err io.Writer
}

type wgCloser struct {
	wg  *sync.WaitGroup
	dir string
}

func (g *wgCloser) Close() error {
	g.wg.Wait()
	if g.dir != "" {
		return os.RemoveAll(g.dir)
	}
	return nil
}
