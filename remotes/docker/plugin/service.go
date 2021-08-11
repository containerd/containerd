package plugin

import (
	api "github.com/containerd/containerd/api/services/remotes/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/remotes/service"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "docker-pusher",
		Requires: []plugin.Type{
			plugin.ServicePlugin,
		},
		InitFn: func(ic *plugin.InitContext) (interface{}, error) {
			local, err := ic.GetByID(plugin.ServicePlugin, dockerPusherPlugin)
			if err != nil {
				return nil, err
			}
			return &pushService{l: local.(service.PushService)}, nil
		},
	})
}

var _ api.PushServiceServer = &pushService{}

type pushService struct {
	l service.PushService
}

func (s *pushService) Push(req *api.PushRequest, srv api.PushService_PushServer) (retErr error) {
	ctx := srv.Context()

	log.G(ctx).Debug("Start pushing")
	defer func() {
		log.G(ctx).WithField("err", retErr).Debug("Done pushing")
	}()

	ch, err := s.l.Push(srv.Context(), req)
	if err != nil {
		return errdefs.ToGRPC(err)
	}

	for status := range ch {
		if err := srv.Send(status.PushResponse); err != nil {
			return err
		}
		if status.Err != nil {
			return errdefs.ToGRPC(status.Err)
		}
	}

	return nil
}

func (s *pushService) Register(srv *grpc.Server) error {
	api.RegisterPushServiceServer(srv, s)
	return nil
}
