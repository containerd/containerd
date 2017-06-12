package containerd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"syscall"

	"github.com/containerd/containerd/api/services/containers"
	"github.com/containerd/containerd/api/services/execution"
	taskapi "github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/rootfs"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const UnknownExitStatus = 255

type TaskStatus string

const (
	Running TaskStatus = "running"
	Created TaskStatus = "created"
	Stopped TaskStatus = "stopped"
	Paused  TaskStatus = "paused"
	Pausing TaskStatus = "pausing"
)

type CheckpointOpts func(*execution.CheckpointRequest) error

type Task interface {
	Pid() uint32
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Pause(context.Context) error
	Resume(context.Context) error
	Start(context.Context) error
	Status(context.Context) (TaskStatus, error)
	Wait(context.Context) (uint32, error)
	Exec(context.Context, *specs.Process, IOCreation) (Process, error)
	Processes(context.Context) ([]uint32, error)
	CloseStdin(context.Context) error
	Resize(ctx context.Context, w, h uint32) error
	IO() *IO
	Checkpoint(context.Context, ...CheckpointOpts) (v1.Descriptor, error)
}

type Process interface {
	Pid() uint32
	Start(context.Context) error
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Wait(context.Context) (uint32, error)
	CloseStdin(context.Context) error
	Resize(ctx context.Context, w, h uint32) error
	IO() *IO
}

var _ = (Task)(&task{})

type task struct {
	client *Client

	io          *IO
	containerID string
	pid         uint32

	deferred *execution.CreateRequest
	pidSync  chan struct{}
}

// Pid returns the pid or process id for the task
func (t *task) Pid() uint32 {
	return t.pid
}

func (t *task) Start(ctx context.Context) error {
	if t.deferred != nil {
		response, err := t.client.TaskService().Create(ctx, t.deferred)
		t.deferred = nil
		if err != nil {
			return err
		}
		t.pid = response.Pid
		close(t.pidSync)
		return nil
	}
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
	return TaskStatus(strings.ToLower(r.Task.Status.String())), nil
}

// Wait is a blocking call that will wait for the task to exit and return the exit status
func (t *task) Wait(ctx context.Context) (uint32, error) {
	events, err := t.client.TaskService().Events(ctx, &execution.EventsRequest{})
	if err != nil {
		return UnknownExitStatus, err
	}
	<-t.pidSync
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
	var cerr error
	if t.io != nil {
		cerr = t.io.Close()
	}
	r, err := t.client.TaskService().Delete(ctx, &execution.DeleteRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return UnknownExitStatus, err
	}
	return r.ExitStatus, cerr
}

func (t *task) Exec(ctx context.Context, spec *specs.Process, ioCreate IOCreation) (Process, error) {
	i, err := ioCreate()
	if err != nil {
		return nil, err
	}
	return &process{
		task:    t,
		io:      i,
		spec:    spec,
		pidSync: make(chan struct{}),
	}, nil
}

func (t *task) Processes(ctx context.Context) ([]uint32, error) {
	response, err := t.client.TaskService().Processes(ctx, &execution.ProcessesRequest{
		ContainerID: t.containerID,
	})
	if err != nil {
		return nil, err
	}
	var out []uint32
	for _, p := range response.Processes {
		out = append(out, p.Pid)
	}
	return out, nil
}

func (t *task) CloseStdin(ctx context.Context) error {
	_, err := t.client.TaskService().CloseStdin(ctx, &execution.CloseStdinRequest{
		ContainerID: t.containerID,
		Pid:         t.pid,
	})
	return err
}

func (t *task) IO() *IO {
	return t.io
}

func (t *task) Resize(ctx context.Context, w, h uint32) error {
	_, err := t.client.TaskService().Pty(ctx, &execution.PtyRequest{
		ContainerID: t.containerID,
		Width:       w,
		Height:      h,
		Pid:         t.pid,
	})
	return err
}

func WithExit(r *execution.CheckpointRequest) error {
	r.Exit = true
	return nil
}

func (t *task) Checkpoint(ctx context.Context, opts ...CheckpointOpts) (d v1.Descriptor, err error) {
	request := &execution.CheckpointRequest{
		ContainerID: t.containerID,
	}
	for _, o := range opts {
		if err := o(request); err != nil {
			return d, err
		}
	}
	// if we are not exiting the container after the checkpoint, make sure we pause it and resume after
	// all other filesystem operations are completed
	if !request.Exit {
		if err := t.Pause(ctx); err != nil {
			return d, err
		}
		defer t.Resume(ctx)
	}
	cr, err := t.client.ContainerService().Get(ctx, &containers.GetContainerRequest{
		ID: t.containerID,
	})
	if err != nil {
		return d, err
	}
	var index v1.Index
	if err := t.checkpointTask(ctx, &index, request); err != nil {
		return d, err
	}
	if err := t.checkpointImage(ctx, &index, cr.Container.Image); err != nil {
		return d, err
	}
	if err := t.checkpointRWSnapshot(ctx, &index, cr.Container.RootFS); err != nil {
		return d, err
	}
	index.Annotations = make(map[string]string)
	index.Annotations["image.name"] = cr.Container.Image
	return t.writeIndex(ctx, &index)
}

func (t *task) checkpointTask(ctx context.Context, index *v1.Index, request *execution.CheckpointRequest) error {
	response, err := t.client.TaskService().Checkpoint(ctx, request)
	if err != nil {
		return err
	}
	// add the checkpoint descriptors to the index
	for _, d := range response.Descriptors {
		index.Manifests = append(index.Manifests, v1.Descriptor{
			MediaType: d.MediaType,
			Size:      d.Size_,
			Digest:    d.Digest,
			Platform: &v1.Platform{
				OS:           runtime.GOOS,
				Architecture: runtime.GOARCH,
			},
		})
	}
	return nil
}

func (t *task) checkpointRWSnapshot(ctx context.Context, index *v1.Index, id string) error {
	rw, err := rootfs.Diff(ctx, id, fmt.Sprintf("checkpoint-rw-%s", id), t.client.SnapshotService(), t.client.DiffService())
	if err != nil {
		return err
	}
	rw.Platform = &v1.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
	}
	index.Manifests = append(index.Manifests, rw)
	return nil
}

func (t *task) checkpointImage(ctx context.Context, index *v1.Index, image string) error {
	if image == "" {
		return fmt.Errorf("cannot checkpoint image with empty name")
	}
	ir, err := t.client.ImageService().Get(ctx, image)
	if err != nil {
		return err
	}
	index.Manifests = append(index.Manifests, ir.Target)
	return nil
}

func (t *task) writeIndex(ctx context.Context, index *v1.Index) (v1.Descriptor, error) {
	buf := bytes.NewBuffer(nil)
	if err := json.NewEncoder(buf).Encode(index); err != nil {
		return v1.Descriptor{}, err
	}
	return writeContent(ctx, t.client.ContentStore(), v1.MediaTypeImageIndex, t.containerID, buf)
}

func writeContent(ctx context.Context, store content.Store, mediaType, ref string, r io.Reader) (d v1.Descriptor, err error) {
	writer, err := store.Writer(ctx, ref, 0, "")
	if err != nil {
		return d, err
	}
	defer writer.Close()
	size, err := io.Copy(writer, r)
	if err != nil {
		return d, err
	}
	if err := writer.Commit(0, ""); err != nil {
		return d, err
	}
	return v1.Descriptor{
		MediaType: mediaType,
		Digest:    writer.Digest(),
		Size:      size,
	}, nil
}
