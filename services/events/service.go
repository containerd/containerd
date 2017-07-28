package events

import (
	api "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/plugin"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "events",
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return NewService(ic.Events), nil
		},
	})
}

type Service struct {
	events *events.Exchange
}

func NewService(events *events.Exchange) api.EventsServer {
	return &Service{events: events}
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterEventsServer(server, s)
	return nil
}

func (s *Service) Publish(ctx context.Context, r *api.PublishRequest) (*empty.Empty, error) {
	if err := s.events.Publish(ctx, r.Topic, r.Event); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (s *Service) Forward(ctx context.Context, r *api.ForwardRequest) (*empty.Empty, error) {
	if err := s.events.Forward(ctx, r.Envelope); err != nil {
		return nil, errdefs.ToGRPC(err)
	}

	return &empty.Empty{}, nil
}

func (s *Service) Subscribe(req *api.SubscribeRequest, srv api.Events_SubscribeServer) error {
	ctx, cancel := context.WithCancel(srv.Context())
	defer cancel()

	eventq, errq := s.events.Subscribe(ctx, req.Filters...)
	for {
		select {
		case ev := <-eventq:
			if err := srv.Send(ev); err != nil {
				return errors.Wrapf(err, "failed sending event to subscriber")
			}
		case err := <-errq:
			if err != nil {
				return errors.Wrapf(err, "subscription error")
			}

			return nil
		}
	}
}
