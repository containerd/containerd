package tasks

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/boltdb/bolt"
	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	api "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/api/types/task"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/plugin"
	protobuf "github.com/gogo/protobuf/types"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	specs "github.com/opencontainers/image-spec/specs-go"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	_     = (api.TasksServer)(&Service{})
	empty = &google_protobuf.Empty{}
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "tasks",
		Requires: []plugin.PluginType{
			plugin.RuntimePlugin,
			plugin.MetadataPlugin,
			plugin.ContentPlugin,
		},
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	rt, err := ic.GetAll(plugin.RuntimePlugin)
	if err != nil {
		return nil, err
	}
	m, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}
	ct, err := ic.Get(plugin.ContentPlugin)
	if err != nil {
		return nil, err
	}
	runtimes := make(map[string]plugin.Runtime)
	for _, rr := range rt {
		r := rr.(plugin.Runtime)
		runtimes[r.ID()] = r
	}
	e := events.GetPoster(ic.Context)
	return &Service{
		runtimes: runtimes,
		db:       m.(*bolt.DB),
		store:    ct.(content.Store),
		emitter:  e,
	}, nil
}

type Service struct {
	runtimes map[string]plugin.Runtime
	db       *bolt.DB
	store    content.Store
	emitter  events.Poster
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterTasksServer(server, s)
	return nil
}

func (s *Service) Create(ctx context.Context, r *api.CreateTaskRequest) (*api.CreateTaskResponse, error) {
	var (
		checkpointPath string
		err            error
	)
	if r.Checkpoint != nil {
		checkpointPath, err = ioutil.TempDir("", "ctrd-checkpoint")
		if err != nil {
			return nil, err
		}
		if r.Checkpoint.MediaType != images.MediaTypeContainerd1Checkpoint {
			return nil, fmt.Errorf("unsupported checkpoint type %q", r.Checkpoint.MediaType)
		}
		reader, err := s.store.Reader(ctx, r.Checkpoint.Digest)
		if err != nil {
			return nil, err
		}
		_, err = archive.Apply(ctx, checkpointPath, reader)
		reader.Close()
		if err != nil {
			return nil, err
		}
	}

	container, err := s.getContainer(ctx, r.ContainerID)
	if err != nil {
		switch {
		case metadata.IsNotFound(err):
			return nil, grpc.Errorf(codes.NotFound, "container %v not found", r.ContainerID)
		case metadata.IsExists(err):
			return nil, grpc.Errorf(codes.AlreadyExists, "container %v already exists", r.ContainerID)
		}

		return nil, err
	}

	opts := plugin.CreateOpts{
		Spec: container.Spec,
		IO: plugin.IO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
		Checkpoint: checkpointPath,
	}
	for _, m := range r.Rootfs {
		opts.Rootfs = append(opts.Rootfs, mount.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}
	runtime, err := s.getRuntime(container.Runtime.Name)
	if err != nil {
		return nil, err
	}
	c, err := runtime.Create(ctx, r.ContainerID, opts)
	if err != nil {
		return nil, errors.Wrap(err, "runtime create failed")
	}
	state, err := c.State(ctx)
	if err != nil {
		log.G(ctx).Error(err)
	}

	if err := s.emit(ctx, "/tasks/create", &eventsapi.TaskCreate{
		ContainerID: r.ContainerID,
	}); err != nil {
		return nil, err
	}

	return &api.CreateTaskResponse{
		ContainerID: r.ContainerID,
		Pid:         state.Pid,
	}, nil
}

func (s *Service) Start(ctx context.Context, r *api.StartTaskRequest) (*google_protobuf.Empty, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	if err := t.Start(ctx); err != nil {
		return nil, err
	}

	if err := s.emit(ctx, "/tasks/start", &eventsapi.TaskStart{
		ContainerID: r.ContainerID,
	}); err != nil {
		return nil, err
	}

	return empty, nil
}

func (s *Service) Delete(ctx context.Context, r *api.DeleteTaskRequest) (*api.DeleteResponse, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	runtime, err := s.getRuntime(t.Info().Runtime)
	if err != nil {
		return nil, err
	}
	exit, err := runtime.Delete(ctx, t)
	if err != nil {
		return nil, err
	}
	if err := s.emit(ctx, "/tasks/delete", &eventsapi.TaskDelete{
		ContainerID: r.ContainerID,
		Pid:         exit.Pid,
		ExitStatus:  exit.Status,
	}); err != nil {
		return nil, err
	}
	return &api.DeleteResponse{
		ExitStatus: exit.Status,
		ExitedAt:   exit.Timestamp,
		Pid:        exit.Pid,
	}, nil
}

func (s *Service) DeleteProcess(ctx context.Context, r *api.DeleteProcessRequest) (*api.DeleteResponse, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	exit, err := t.DeleteProcess(ctx, r.Pid)
	if err != nil {
		return nil, err
	}
	return &api.DeleteResponse{
		ExitStatus: exit.Status,
		ExitedAt:   exit.Timestamp,
		Pid:        exit.Pid,
	}, nil
}

func taskFromContainerd(ctx context.Context, c plugin.Task) (*task.Task, error) {
	state, err := c.State(ctx)
	if err != nil {
		return nil, err
	}

	var status task.Status
	switch state.Status {
	case plugin.CreatedStatus:
		status = task.StatusCreated
	case plugin.RunningStatus:
		status = task.StatusRunning
	case plugin.StoppedStatus:
		status = task.StatusStopped
	case plugin.PausedStatus:
		status = task.StatusPaused
	default:
		log.G(ctx).WithField("status", state.Status).Warn("unknown status")
	}
	return &task.Task{
		ID:          c.Info().ID,
		ContainerID: c.Info().ContainerID,
		Pid:         state.Pid,
		Status:      status,
		Spec: &protobuf.Any{
			TypeUrl: specs.Version,
			Value:   c.Info().Spec,
		},
		Stdin:    state.Stdin,
		Stdout:   state.Stdout,
		Stderr:   state.Stderr,
		Terminal: state.Terminal,
	}, nil
}

func (s *Service) Get(ctx context.Context, r *api.GetTaskRequest) (*api.GetTaskResponse, error) {
	task, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	t, err := taskFromContainerd(ctx, task)
	if err != nil {
		return nil, err
	}
	return &api.GetTaskResponse{
		Task: t,
	}, nil
}

func (s *Service) List(ctx context.Context, r *api.ListTasksRequest) (*api.ListTasksResponse, error) {
	resp := &api.ListTasksResponse{}
	for _, r := range s.runtimes {
		tasks, err := r.Tasks(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			tt, err := taskFromContainerd(ctx, t)
			if err != nil {
				return nil, err
			}
			resp.Tasks = append(resp.Tasks, tt)
		}
	}
	return resp, nil
}

func (s *Service) Pause(ctx context.Context, r *api.PauseTaskRequest) (*google_protobuf.Empty, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	err = t.Pause(ctx)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Resume(ctx context.Context, r *api.ResumeTaskRequest) (*google_protobuf.Empty, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	err = t.Resume(ctx)
	if err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) Kill(ctx context.Context, r *api.KillRequest) (*google_protobuf.Empty, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}

	switch v := r.PidOrAll.(type) {
	case *api.KillRequest_All:
		if err := t.Kill(ctx, r.Signal, 0, true); err != nil {
			return nil, err
		}
	case *api.KillRequest_Pid:
		if err := t.Kill(ctx, r.Signal, v.Pid, false); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid option specified; expected pid or all")
	}
	return empty, nil
}

func (s *Service) ListProcesses(ctx context.Context, r *api.ListProcessesRequest) (*api.ListProcessesResponse, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}

	pids, err := t.Processes(ctx)
	if err != nil {
		return nil, err
	}

	ps := []*task.Process{}
	for _, pid := range pids {
		ps = append(ps, &task.Process{
			Pid: pid,
		})
	}
	return &api.ListProcessesResponse{
		Processes: ps,
	}, nil
}

func (s *Service) Exec(ctx context.Context, r *api.ExecProcessRequest) (*api.ExecProcessResponse, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	process, err := t.Exec(ctx, plugin.ExecOpts{
		Spec: r.Spec.Value,
		IO: plugin.IO{
			Stdin:    r.Stdin,
			Stdout:   r.Stdout,
			Stderr:   r.Stderr,
			Terminal: r.Terminal,
		},
	})
	if err != nil {
		return nil, err
	}
	state, err := process.State(ctx)
	if err != nil {
		return nil, err
	}
	return &api.ExecProcessResponse{
		Pid: state.Pid,
	}, nil
}

func (s *Service) ResizePty(ctx context.Context, r *api.ResizePtyRequest) (*google_protobuf.Empty, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	if err := t.ResizePty(ctx, r.Pid, plugin.ConsoleSize{
		Width:  r.Width,
		Height: r.Height,
	}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) CloseIO(ctx context.Context, r *api.CloseIORequest) (*google_protobuf.Empty, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	if r.Stdin {
		if err := t.CloseIO(ctx, r.Pid); err != nil {
			return nil, err
		}
	}
	return empty, nil
}

func (s *Service) Checkpoint(ctx context.Context, r *api.CheckpointTaskRequest) (*api.CheckpointTaskResponse, error) {
	t, err := s.getTask(ctx, r.ContainerID)
	if err != nil {
		return nil, err
	}
	image, err := ioutil.TempDir("", "ctd-checkpoint")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(image)
	if err := t.Checkpoint(ctx, image, r.Options); err != nil {
		return nil, err
	}
	// write checkpoint to the content store
	tar := archive.Diff(ctx, "", image)
	cp, err := s.writeContent(ctx, images.MediaTypeContainerd1Checkpoint, image, tar)
	// close tar first after write
	if err := tar.Close(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	// write the config to the content store
	spec := bytes.NewReader(t.Info().Spec)
	specD, err := s.writeContent(ctx, images.MediaTypeContainerd1CheckpointConfig, filepath.Join(image, "spec"), spec)
	if err != nil {
		return nil, err
	}
	return &api.CheckpointTaskResponse{
		Descriptors: []*types.Descriptor{
			cp,
			specD,
		},
	}, nil
}

func (s *Service) writeContent(ctx context.Context, mediaType, ref string, r io.Reader) (*types.Descriptor, error) {
	writer, err := s.store.Writer(ctx, ref, 0, "")
	if err != nil {
		return nil, err
	}
	defer writer.Close()
	size, err := io.Copy(writer, r)
	if err != nil {
		return nil, err
	}
	if err := writer.Commit(0, ""); err != nil {
		return nil, err
	}
	return &types.Descriptor{
		MediaType: mediaType,
		Digest:    writer.Digest(),
		Size_:     size,
	}, nil
}

func (s *Service) getContainer(ctx context.Context, id string) (containers.Container, error) {
	var container containers.Container

	if err := s.db.View(func(tx *bolt.Tx) error {
		store := metadata.NewContainerStore(tx)
		var err error
		container, err = store.Get(ctx, id)
		return err
	}); err != nil {
		return containers.Container{}, err
	}

	return container, nil
}

func (s *Service) getTask(ctx context.Context, id string) (plugin.Task, error) {
	container, err := s.getContainer(ctx, id)
	if err != nil {
		return nil, grpc.Errorf(codes.InvalidArgument, "task %v not found: %s", id, err.Error())
	}

	runtime, err := s.getRuntime(container.Runtime.Name)
	if err != nil {
		return nil, grpc.Errorf(codes.NotFound, "task %v not found: %s", id, err.Error())
	}

	t, err := runtime.Get(ctx, id)
	if err != nil {
		return nil, grpc.Errorf(codes.NotFound, "task %v not found", id)
	}

	return t, nil
}

func (s *Service) getRuntime(name string) (plugin.Runtime, error) {
	runtime, ok := s.runtimes[name]
	if !ok {
		return nil, fmt.Errorf("unknown runtime %q", name)
	}
	return runtime, nil
}

func (s *Service) emit(ctx context.Context, topic string, evt interface{}) error {
	emitterCtx := events.WithTopic(ctx, topic)
	if err := s.emitter.Post(emitterCtx, evt); err != nil {
		return err
	}

	return nil
}
