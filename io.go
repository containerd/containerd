package containerd

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/containerd/fifo"
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

// BufferedIO returns IO that will be logged to an in memory buffer
func BufferedIO(stdin, stdout, stderr *bytes.Buffer) IOCreation {
	return func() (*IO, error) {
		paths, err := fifoPaths()
		if err != nil {
			return nil, err
		}
		i := &IO{
			Terminal: false,
			Stdout:   paths.out,
			Stderr:   paths.err,
			Stdin:    paths.in,
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
	paths, err := fifoPaths()
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
		Stdin:    paths.in,
		Stdout:   paths.out,
		Stderr:   paths.err,
		closer:   closer,
	}, nil
}

func fifoPaths() (*fifoSet, error) {
	root := filepath.Join(os.TempDir(), "containerd")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := ioutil.TempDir(root, "")
	if err != nil {
		return nil, err
	}
	return &fifoSet{
		dir: dir,
		in:  filepath.Join(dir, "stdin"),
		out: filepath.Join(dir, "stdout"),
		err: filepath.Join(dir, "stderr"),
	}, nil
}

type fifoSet struct {
	// dir is the directory holding the task fifos
	dir          string
	in, out, err string
}

type ioSet struct {
	in       io.Reader
	out, err io.Writer
}

func copyIO(fifos *fifoSet, ioset *ioSet, tty bool) (closer io.Closer, err error) {
	var (
		f   io.ReadWriteCloser
		ctx = context.Background()
		wg  = &sync.WaitGroup{}
	)

	if f, err = fifo.OpenFifo(ctx, fifos.in, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	go func(w io.WriteCloser) {
		io.Copy(w, ioset.in)
		w.Close()
	}(f)

	if f, err = fifo.OpenFifo(ctx, fifos.out, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)
	wg.Add(1)
	go func(r io.ReadCloser) {
		io.Copy(ioset.out, r)
		r.Close()
		wg.Done()
	}(f)

	if f, err = fifo.OpenFifo(ctx, fifos.err, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700); err != nil {
		return nil, err
	}
	defer func(c io.Closer) {
		if err != nil {
			c.Close()
		}
	}(f)

	if !tty {
		wg.Add(1)
		go func(r io.ReadCloser) {
			io.Copy(ioset.err, r)
			r.Close()
			wg.Done()
		}(f)
	}

	return &wgCloser{
		wg:  wg,
		dir: fifos.dir,
	}, nil
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
