package containerd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"syscall"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/linux/runcopts"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/rootfs"
	"github.com/containerd/containerd/typeurl"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go/v1"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
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

type IOCloseInfo struct {
	Stdin bool
}

type IOCloserOpts func(*IOCloseInfo)

func WithStdinCloser(r *IOCloseInfo) {
	r.Stdin = true
}

type CheckpointTaskInfo struct {
	ParentCheckpoint digest.Digest
	Options          interface{}
}

type CheckpointTaskOpts func(*CheckpointTaskInfo) error

type TaskInfo struct {
	Checkpoint *types.Descriptor
	RootFS     []mount.Mount
	Options    interface{}
}

type Task interface {
	Pid() uint32
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Pause(context.Context) error
	Resume(context.Context) error
	Start(context.Context) error
	Status(context.Context) (TaskStatus, error)
	Wait(context.Context) (uint32, error)
	Exec(context.Context, string, *specs.Process, IOCreation) (Process, error)
	Pids(context.Context) ([]uint32, error)
	CloseIO(context.Context, ...IOCloserOpts) error
	Resize(ctx context.Context, w, h uint32) error
	IO() *IO
	Checkpoint(context.Context, ...CheckpointTaskOpts) (v1.Descriptor, error)
	Update(context.Context, ...UpdateTaskOpts) error
}

type Process interface {
	Pid() uint32
	Start(context.Context) error
	Delete(context.Context) (uint32, error)
	Kill(context.Context, syscall.Signal) error
	Wait(context.Context) (uint32, error)
	CloseIO(context.Context, ...IOCloserOpts) error
	Resize(ctx context.Context, w, h uint32) error
	IO() *IO
}

var _ = (Task)(&task{})

type task struct {
	client *Client

	io  *IO
	id  string
	pid uint32

	deferred *tasks.CreateTaskRequest
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
			t.io.closer.Close()
			return err
		}
		t.pid = response.Pid
		return nil
	}
	_, err := t.client.TaskService().Start(ctx, &tasks.StartTaskRequest{
		ContainerID: t.id,
	})
	if err != nil {
		t.io.closer.Close()
	}
	return err
}

func (t *task) Kill(ctx context.Context, s syscall.Signal) error {
	_, err := t.client.TaskService().Kill(ctx, &tasks.KillRequest{
		Signal:      uint32(s),
		ContainerID: t.id,
	})
	if err != nil {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (t *task) Pause(ctx context.Context) error {
	_, err := t.client.TaskService().Pause(ctx, &tasks.PauseTaskRequest{
		ContainerID: t.id,
	})
	return errdefs.FromGRPC(err)
}

func (t *task) Resume(ctx context.Context) error {
	_, err := t.client.TaskService().Resume(ctx, &tasks.ResumeTaskRequest{
		ContainerID: t.id,
	})
	return errdefs.FromGRPC(err)
}

func (t *task) Status(ctx context.Context) (TaskStatus, error) {
	r, err := t.client.TaskService().Get(ctx, &tasks.GetTaskRequest{
		ContainerID: t.id,
	})
	if err != nil {
		return "", errdefs.FromGRPC(err)
	}
	return TaskStatus(strings.ToLower(r.Task.Status.String())), nil
}

// Wait is a blocking call that will wait for the task to exit and return the exit status
func (t *task) Wait(ctx context.Context) (uint32, error) {
	eventstream, err := t.client.EventService().Stream(ctx, &eventsapi.StreamEventsRequest{})
	if err != nil {
		return UnknownExitStatus, errdefs.FromGRPC(err)
	}
	for {
		evt, err := eventstream.Recv()
		if err != nil {
			return UnknownExitStatus, err
		}
		if typeurl.Is(evt.Event, &eventsapi.TaskExit{}) {
			v, err := typeurl.UnmarshalAny(evt.Event)
			if err != nil {
				return UnknownExitStatus, err
			}
			e := v.(*eventsapi.TaskExit)
			if e.ContainerID == t.id && e.Pid == t.pid {
				return e.ExitStatus, nil
			}
		}
	}
}

// Delete deletes the task and its runtime state
// it returns the exit status of the task and any errors that were encountered
// during cleanup
func (t *task) Delete(ctx context.Context) (uint32, error) {
	if t.io != nil {
		t.io.Cancel()
		t.io.Wait()
		t.io.Close()
	}
	r, err := t.client.TaskService().Delete(ctx, &tasks.DeleteTaskRequest{
		ContainerID: t.id,
	})
	if err != nil {
		return UnknownExitStatus, err
	}
	return r.ExitStatus, nil
}

func (t *task) Exec(ctx context.Context, id string, spec *specs.Process, ioCreate IOCreation) (Process, error) {
	if id == "" {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument, "exec id must not be empty")
	}
	i, err := ioCreate(id)
	if err != nil {
		return nil, err
	}
	return &process{
		id:   id,
		task: t,
		io:   i,
		spec: spec,
	}, nil
}

func (t *task) Pids(ctx context.Context) ([]uint32, error) {
	response, err := t.client.TaskService().ListPids(ctx, &tasks.ListPidsRequest{
		ContainerID: t.id,
	})
	if err != nil {
		return nil, err
	}
	return response.Pids, nil
}

func (t *task) CloseIO(ctx context.Context, opts ...IOCloserOpts) error {
	r := &tasks.CloseIORequest{
		ContainerID: t.id,
	}
	var i IOCloseInfo
	for _, o := range opts {
		o(&i)
	}
	r.Stdin = i.Stdin
	_, err := t.client.TaskService().CloseIO(ctx, r)
	return err
}

func (t *task) IO() *IO {
	return t.io
}

func (t *task) Resize(ctx context.Context, w, h uint32) error {
	_, err := t.client.TaskService().ResizePty(ctx, &tasks.ResizePtyRequest{
		ContainerID: t.id,
		Width:       w,
		Height:      h,
	})
	return err
}

func (t *task) Checkpoint(ctx context.Context, opts ...CheckpointTaskOpts) (d v1.Descriptor, err error) {
	request := &tasks.CheckpointTaskRequest{
		ContainerID: t.id,
	}
	var i CheckpointTaskInfo
	for _, o := range opts {
		if err := o(&i); err != nil {
			return d, err
		}
	}
	request.ParentCheckpoint = i.ParentCheckpoint
	if i.Options != nil {
		any, err := typeurl.MarshalAny(i.Options)
		if err != nil {
			return d, err
		}
		request.Options = any
	}
	// make sure we pause it and resume after all other filesystem operations are completed
	if err := t.Pause(ctx); err != nil {
		return d, err
	}
	defer t.Resume(ctx)
	cr, err := t.client.ContainerService().Get(ctx, t.id)
	if err != nil {
		return d, err
	}
	var index v1.Index
	if err := t.checkpointTask(ctx, &index, request); err != nil {
		return d, err
	}
	if err := t.checkpointImage(ctx, &index, cr.Image); err != nil {
		return d, err
	}
	if err := t.checkpointRWSnapshot(ctx, &index, cr.Snapshotter, cr.RootFS); err != nil {
		return d, err
	}
	index.Annotations = make(map[string]string)
	index.Annotations["image.name"] = cr.Image
	return t.writeIndex(ctx, &index)
}

type UpdateTaskInfo struct {
	Resources interface{}
}

type UpdateTaskOpts func(context.Context, *Client, *UpdateTaskInfo) error

func (t *task) Update(ctx context.Context, opts ...UpdateTaskOpts) error {
	request := &tasks.UpdateTaskRequest{
		ContainerID: t.id,
	}
	var i UpdateTaskInfo
	for _, o := range opts {
		if err := o(ctx, t.client, &i); err != nil {
			return err
		}
	}
	if i.Resources != nil {
		any, err := typeurl.MarshalAny(i.Resources)
		if err != nil {
			return err
		}
		request.Resources = any
	}
	_, err := t.client.TaskService().Update(ctx, request)
	return err
}

func (t *task) checkpointTask(ctx context.Context, index *v1.Index, request *tasks.CheckpointTaskRequest) error {
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
				OS:           goruntime.GOOS,
				Architecture: goruntime.GOARCH,
			},
		})
	}
	return nil
}

func (t *task) checkpointRWSnapshot(ctx context.Context, index *v1.Index, snapshotterName string, id string) error {
	rw, err := rootfs.Diff(ctx, id, fmt.Sprintf("checkpoint-rw-%s", id), t.client.SnapshotService(snapshotterName), t.client.DiffService())
	if err != nil {
		return err
	}
	rw.Platform = &v1.Platform{
		OS:           goruntime.GOOS,
		Architecture: goruntime.GOARCH,
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
	return writeContent(ctx, t.client.ContentStore(), v1.MediaTypeImageIndex, t.id, buf)
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
	if err := writer.Commit(size, ""); err != nil {
		return d, err
	}
	return v1.Descriptor{
		MediaType: mediaType,
		Digest:    writer.Digest(),
		Size:      size,
	}, nil
}

func WithExit(r *CheckpointTaskInfo) error {
	r.Options = &runcopts.CheckpointOptions{
		Exit: true,
	}
	return nil
}
