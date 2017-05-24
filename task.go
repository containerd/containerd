package containerd

import (
	"context"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/containerd/containerd/api/services/execution"
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

// STDIO returns an IO implementation to be used for a task
// that outputs the container's IO as the current processes STDIO
func STDIO() (*IO, error) {
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
	dir, err := ioutil.TempDir(filepath.Join(os.TempDir(), "containerd"), "")
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
		ctx = context.Background()
		wg  = &sync.WaitGroup{}
	)

	f, err := fifo.OpenFifo(ctx, fifos.in, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	if err != nil {
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

	f, err = fifo.OpenFifo(ctx, fifos.out, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	if err != nil {
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

	f, err = fifo.OpenFifo(ctx, fifos.err, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0700)
	if err != nil {
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
		wg: wg,
	}, nil
}

type wgCloser struct {
	wg *sync.WaitGroup
}

func (g *wgCloser) Close() error {
	g.wg.Wait()
	return nil
}

type Task struct {
	client *Client

	io          *IO
	containerID string
	pid         uint32
}

// Pid returns the pid or process id for the task
func (t *Task) Pid() uint32 {
	return t.pid
}

func (t *Task) Kill(ctx context.Context, s os.Signal) error {
	_, err := t.client.tasks().Kill(ctx, &execution.KillRequest{
		ContainerID: t.containerID,
		PidOrAll: &execution.KillRequest_All{
			All: true,
		},
	})
	return err
}

// Delete deletes the task and its runtime state
// it returns the exit status of the task and any errors that were encountered
// during cleanup
func (t *Task) Delete(ctx context.Context) (uint32, error) {
	cerr := t.io.Close()
	r, err := t.client.tasks().Delete(ctx, &execution.DeleteRequest{
		t.containerID,
	})
	if err != nil {
		return 255, err
	}
	return r.ExitStatus, cerr
}
