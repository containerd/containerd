package version

import (
	api "github.com/containerd/containerd/api/services/version"
	"github.com/containerd/containerd/plugin"
	ctrdversion "github.com/containerd/containerd/version"
	empty "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var _ api.VersionServer = &Service{}

func init() {
	plugin.Register("version-grpc", &plugin.Registration{
		Type: plugin.GRPCPlugin,
		Init: New,
	})
}

func New(ic *plugin.InitContext) (interface{}, error) {
	return &Service{}, nil
}

type Service struct {
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterVersionServer(server, s)
	return nil
}

func (s *Service) Version(ctx context.Context, _ *empty.Empty) (*api.VersionResponse, error) {
	return &api.VersionResponse{
		Version:  ctrdversion.Version,
		Revision: ctrdversion.Revision,
	}, nil
}
