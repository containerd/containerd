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
	taskapi "github.com/containerd/containerd/api/types/task"
	"github.com/containerd/fifo"
)

const UnknownExitStatus = 255

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

type TaskStatus string

const (
	Running TaskStatus = "running"
	Created TaskStatus = "created"
	Stopped TaskStatus = "stopped"
	Paused  TaskStatus = "paused"
	Pausing TaskStatus = "pausing"
)

type Task interface {
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Pause(context.Context) error
	Resume(context.Context) error
	Pid() uint32
	Start(context.Context) error
	Status(context.Context) (TaskStatus, error)
	Wait(context.Context) (uint32, error)
}

var _ = (Task)(&task{})

type task struct {
	client *Client

	io          *IO
	containerID string
	pid         uint32
}

// Pid returns the pid or process id for the task
func (t *task) Pid() uint32 {
	return t.pid
}

func (t *task) Start(ctx context.Context) error {
	_, err := t.client.TaskService().Start(ctx, &execution.StartRequest{
		ContainerID: t.containerID,
	})
	return err
}

func (t *task) Kill(ctx context.Context, s syscall.Signal) error {
	_, err := t.client.TaskService().Kill(ctx, &execution.KillRequest{
		Signal:      uint32(s),
		ContainerID: t.containerID,
		PidOrAll: &execution.KillRequest_All{
			All: true,
		},
	})
	return err
}

func (t *task) Pause(ctx context.Context) error {
	_, err := t.client.TaskService().Pause(ctx, &execution.PauseRequest{
		ContainerID: t.containerID,
	})
	return err
}

func (t *task) Resume(ctx context.Context) error {
	_, err := t.client.TaskService().Resume(ctx, &execution.ResumeRequest{
		ContainerID: t.containerID,
	})
	return err
}

func (t *task) Status(ctx context.Context) (TaskStatus, error) {
	r, err := t.client.TaskService().Info(ctx, &execution.InfoRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return "", err
	}
	return TaskStatus(r.Task.Status.String()), nil
}

// Wait is a blocking call that will wait for the task to exit and return the exit status
func (t *task) Wait(ctx context.Context) (uint32, error) {
	events, err := t.client.TaskService().Events(ctx, &execution.EventsRequest{})
	if err != nil {
		return UnknownExitStatus, err
	}
	for {
		e, err := events.Recv()
		if err != nil {
			return UnknownExitStatus, err
		}
		if e.Type != taskapi.Event_EXIT {
			continue
		}
		if e.ID == t.containerID && e.Pid == t.pid {
			return e.ExitStatus, nil
		}
	}
}

// Delete deletes the task and its runtime state
// it returns the exit status of the task and any errors that were encountered
// during cleanup
func (t *task) Delete(ctx context.Context) (uint32, error) {
	cerr := t.io.Close()
	r, err := t.client.TaskService().Delete(ctx, &execution.DeleteRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return UnknownExitStatus, err
	}
	return r.ExitStatus, cerr
}
