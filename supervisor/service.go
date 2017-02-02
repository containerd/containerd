package supervisor

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sync"

	api "github.com/docker/containerd/api/execution"
	"github.com/docker/containerd/api/shim"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
)

var (
	_     = (api.ExecutionServiceServer)(&Service{})
	empty = &google_protobuf.Empty{}
)

// New creates a new GRPC services for execution
func New(ctx context.Context, root string) (*Service, error) {
	clients, err := loadClients(root)
	if err != nil {
		return nil, err
	}
	s := &Service{
		root:  root,
		shims: clients,
	}
	for _, c := range clients {
		if err := s.monitor(c); err != nil {
			return nil, err
		}
	}
	return s, nil
}

type Service struct {
	mu sync.Mutex

	root  string
	shims map[string]shim.ShimClient
}

func (s *Service) CreateContainer(ctx context.Context, r *api.CreateContainerRequest) (*api.CreateContainerResponse, error) {
	s.mu.Lock()
	if _, ok := s.shims[r.ID]; ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("container already exists %q", r.ID)
	}
	client, err := newShimClient(filepath.Join(s.root, r.ID))
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.shims[r.ID] = client
	s.mu.Unlock()
	if err := s.monitor(client); err != nil {
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
		return nil, err
	}
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
		Pid: r.Pid,
	})
	if err != nil {
		return nil, err
	}
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
	panic("not implemented")
}

// containerd managed execs + system pids forked in container
func (s *Service) GetProcess(ctx context.Context, r *api.GetProcessRequest) (*api.GetProcessResponse, error) {
	panic("not implemented")
}

func (s *Service) SignalProcess(ctx context.Context, r *api.SignalProcessRequest) (*google_protobuf.Empty, error) {
	panic("not implemented")
}

func (s *Service) DeleteProcess(ctx context.Context, r *api.DeleteProcessRequest) (*google_protobuf.Empty, error) {
	panic("not implemented")
}

func (s *Service) ListProcesses(ctx context.Context, r *api.ListProcessesRequest) (*api.ListProcessesResponse, error) {
	panic("not implemented")
}

// monitor monitors the shim's event rpc and forwards container and process
// events to callers
func (s *Service) monitor(client shim.ShimClient) error {
	return nil
}

func (s *Service) getShim(id string) (shim.ShimClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.shims[id]
	if !ok {
		return nil, fmt.Errorf("container does not exist %q", id)
	}
	return client, nil
}

func loadClients(root string) (map[string]shim.ShimClient, error) {
	files, err := ioutil.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string]shim.ShimClient)
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		socket := filepath.Join(root, f.Name(), "shim.sock")
		client, err := connectToShim(socket)
		if err != nil {
			return nil, err
		}
		out[f.Name()] = client
	}
	return out, nil
}
