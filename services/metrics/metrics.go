package metrics

import (
	"github.com/containerd/containerd/plugin"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register("metrics-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: New,
	})
}

func New(_ *plugin.InitContext) (interface{}, error) {
	return &Service{}, nil
}

type Service struct {
}

func (s *Service) Register(server *grpc.Server) error {
	grpc_prometheus.Register(server)
	return nil
}
