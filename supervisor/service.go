package supervisor

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	api "github.com/docker/containerd/api/execution"
	"github.com/docker/containerd/api/shim"
	"github.com/docker/containerd/events"
	"github.com/docker/containerd/execution"
	"github.com/docker/containerd/log"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

var (
	_     = (api.ExecutionServiceServer)(&Service{})
	empty = &google_protobuf.Empty{}
)

// New creates a new GRPC services for execution
func New(ctx context.Context, root string) (*Service, error) {
	ctx = log.WithModule(ctx, "supervisor")
	log.G(ctx).WithField("root", root).Debugf("New()")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.Wrapf(err, "unable to create root directory %q", root)
	}
	clients, err := loadClients(ctx, root)
	if err != nil {
		return nil, err
	}
	s := &Service{
		root:  root,
		shims: clients,
		ctx:   ctx,
	}
	for _, c := range clients {
		if err := s.monitor(events.GetPoster(ctx), c); err != nil {
			return nil, err
		}
	}
	return s, nil
}

type Service struct {
	mu sync.Mutex

	ctx   context.Context
	root  string
	shims map[string]*shimClient
}

func (s *Service) CreateContainer(ctx context.Context, r *api.CreateContainerRequest) (*api.CreateContainerResponse, error) {
	client, err := s.newShim(r.ID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			clientLog, clientLogErr := client.GetLog(ctx, &shim.GetLogRequest{})
			if clientLogErr != nil {
				log.G(s.ctx).WithError(clientLogErr).WithField("container", client.id).
					Warnf("failed to get shim log")
			} else {
				log.G(s.ctx).WithField("container", client.id).
					Debugf("shim log: %s", string(clientLog.Log))
			}
			s.removeShim(r.ID)
		}
	}()

	if err := s.monitor(events.GetPoster(ctx), client); err != nil {
		return nil, err
	}
	createResponse, err := client.Create(ctx, &shim.CreateRequest{
		ID:       r.ID,
		Bundle:   r.BundlePath,
		Terminal: r.Console,
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "shim create request failed")
	}
	client.initPid = createResponse.Pid
	return &api.CreateContainerResponse{
		Container: &api.Container{
			ID: r.ID,
		},
		InitProcess: &api.Process{
			Pid: createResponse.Pid,
		},
	}, nil
}

func (s *Service) StartContainer(ctx context.Context, r *api.StartContainerRequest) (*google_protobuf.Empty, error) {
	client, err := s.getShim(r.ID)
	if err != nil {
		return nil, err
	}
	if _, err := client.Start(ctx, &shim.StartRequest{}); err != nil {
		return nil, err
	}
	return empty, nil
}

func (s *Service) DeleteContainer(ctx context.Context, r *api.DeleteContainerRequest) (*google_protobuf.Empty, error) {
	client, err := s.getShim(r.ID)
	if err != nil {
		return nil, err
	}
	_, err = client.Delete(ctx, &shim.DeleteRequest{
		Pid: client.initPid,
	})
	if err != nil {
		return nil, err
	}
	s.removeShim(r.ID)
	return empty, nil
}

func (s *Service) ListContainers(ctx context.Context, r *api.ListContainersRequest) (*api.ListContainersResponse, error) {
	resp := &api.ListContainersResponse{}
	for _, client := range s.shims {
		status, err := client.State(ctx, &shim.StateRequest{})
		if err != nil {
			return nil, err
		}
		resp.Containers = append(resp.Containers, &api.Container{
			ID:     status.ID,
			Bundle: status.Bundle,
		})
	}
	return resp, nil
}
func (s *Service) GetContainer(ctx context.Context, r *api.GetContainerRequest) (*api.GetContainerResponse, error) {
	client, err := s.getShim(r.ID)
	if err != nil {
		return nil, err
	}
	state, err := client.State(ctx, &shim.StateRequest{})
	if err != nil {
		return nil, err
	}
	return &api.GetContainerResponse{
		Container: &api.Container{
			ID:     state.ID,
			Bundle: state.Bundle,
			// TODO: add processes
		},
	}, nil
}

func (s *Service) UpdateContainer(ctx context.Context, r *api.UpdateContainerRequest) (*google_protobuf.Empty, error) {
	panic("not implemented")
	return empty, nil
}

func (s *Service) PauseContainer(ctx context.Context, r *api.PauseContainerRequest) (*google_protobuf.Empty, error) {
	client, err := s.getShim(r.ID)
	if err != nil {
		return nil, err
	}
	return client.Pause(ctx, &shim.PauseRequest{})
}

func (s *Service) ResumeContainer(ctx context.Context, r *api.ResumeContainerRequest) (*google_protobuf.Empty, error) {
	client, err := s.getShim(r.ID)
	if err != nil {
		return nil, err
	}
	return client.Resume(ctx, &shim.ResumeRequest{})
}

func (s *Service) StartProcess(ctx context.Context, r *api.StartProcessRequest) (*api.StartProcessResponse, error) {
	client, err := s.getShim(r.ContainerID)
	if err != nil {
		return nil, err
	}

	er := &shim.ExecRequest{
		Terminal: r.Console,
		Stdin:    r.Stdin,
		Stdout:   r.Stdout,
		Stderr:   r.Stderr,
		Args:     r.Process.Args,
		Env:      r.Process.Env,
		Cwd:      r.Process.Cwd,
	}

	if r.Process.User != nil {
		er.User.Uid = r.Process.User.Uid
		er.User.Gid = r.Process.User.Gid
		er.User.AdditionalGids = r.Process.User.AdditionalGids
	}

	resp, err := client.Exec(ctx, er)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to exec into container %q", r.ContainerID)
	}
	r.Process.Pid = resp.Pid
	return &api.StartProcessResponse{
		Process: r.Process,
	}, nil
}

// containerd managed execs + system pids forked in container
func (s *Service) GetProcess(ctx context.Context, r *api.GetProcessRequest) (*api.GetProcessResponse, error) {
	panic("not implemented")
}

func (s *Service) SignalProcess(ctx context.Context, r *api.SignalProcessRequest) (*google_protobuf.Empty, error) {
	panic("not implemented")
}

func (s *Service) DeleteProcess(ctx context.Context, r *api.DeleteProcessRequest) (*google_protobuf.Empty, error) {
	client, err := s.getShim(r.ContainerID)
	if err != nil {
		return nil, err
	}
	_, err = client.Delete(ctx, &shim.DeleteRequest{
		Pid: r.Pid,
	})
	if err != nil {
		return nil, err
	}
	if r.Pid == client.initPid {
		s.removeShim(r.ContainerID)
	}
	return empty, nil
}

func (s *Service) ListProcesses(ctx context.Context, r *api.ListProcessesRequest) (*api.ListProcessesResponse, error) {
	panic("not implemented")
}

// monitor monitors the shim's event rpc and forwards container and process
// events to callers
func (s *Service) monitor(poster events.Poster, client *shimClient) error {
	// we use the service context here because we don't want to be
	// tied to the Create rpc call
	stream, err := client.Events(s.ctx, &shim.EventsRequest{})
	if err != nil {
		return errors.Wrapf(err, "failed to get events stream for client at %q", client.root)
	}

	go func() {
		for {
			e, err := stream.Recv()
			if err != nil {
				if err.Error() == "EOF" || strings.Contains(err.Error(), "transport is closing") {
					break
				}
				log.G(s.ctx).WithError(err).WithField("container", client.id).
					Warnf("event stream for client at %q got terminated", client.root)
				break
			}

			var topic string
			if e.Type == shim.EventType_CREATE {
				topic = "containers"
			} else {
				topic = fmt.Sprintf("containers.%s", e.ID)
			}

			ctx := events.WithTopic(s.ctx, topic)
			poster.Post(ctx, execution.ContainerEvent{
				Timestamp:  time.Now(),
				ID:         e.ID,
				Type:       toExecutionEventType(e.Type),
				Pid:        e.Pid,
				ExitStatus: e.ExitStatus,
			})
		}
	}()
	return nil
}

func (s *Service) newShim(id string) (*shimClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.shims[id]; ok {
		return nil, errors.Errorf("container %q already exists", id)
	}
	client, err := newShimClient(filepath.Join(s.root, id), id)
	if err != nil {
		return nil, err
	}
	s.shims[id] = client
	return client, nil
}

func (s *Service) getShim(id string) (*shimClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	client, ok := s.shims[id]
	if !ok {
		return nil, fmt.Errorf("container does not exist %q", id)
	}
	return client, nil
}

func (s *Service) removeShim(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	client, ok := s.shims[id]
	if ok {
		client.stop()
		delete(s.shims, id)
	}
}

func loadClients(ctx context.Context, root string) (map[string]*shimClient, error) {
	files, err := ioutil.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*shimClient)
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		//
		id := f.Name()
		client, err := loadShimClient(filepath.Join(root, id), id)
		if err != nil {
			log.G(ctx).WithError(err).WithField("id", id).Warn("failed to load container")
			// TODO: send an exit event with 255 as exit status
			continue
		}
		out[f.Name()] = client
	}
	return out, nil
}

func toExecutionEventType(et shim.EventType) string {
	return strings.Replace(strings.ToLower(et.String()), "_", "-", -1)
}
