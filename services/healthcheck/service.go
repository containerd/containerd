package healthcheck

import (
	"github.com/containerd/containerd/plugin"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Service struct {
	serve *health.Server
}

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "healthcheck",
		InitFn: func(*plugin.InitContext) (interface{}, error) {
			return NewService()
		},
	})
}

func NewService() (*Service, error) {
	return &Service{
		health.NewServer(),
	}, nil
}

func (s *Service) Register(server *grpc.Server) error {
	grpc_health_v1.RegisterHealthServer(server, s.serve)
	return nil
}
