package events

import (
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	api "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/plugin"
	"google.golang.org/grpc"
)

func init() {
	plugin.Register(&plugin.Registration{
		Type: plugin.GRPCPlugin,
		ID:   "events",
		Init: func(ic *plugin.InitContext) (interface{}, error) {
			return NewService(ic.Emitter), nil
		},
	})
}

type Service struct {
	emitter  *events.Emitter
	timeouts map[string]*time.Timer
}

func NewService(e *events.Emitter) api.EventsServer {
	return &Service{emitter: e}
}

func (s *Service) Register(server *grpc.Server) error {
	api.RegisterEventsServer(server, s)
	return nil
}

func (s *Service) Stream(req *api.StreamEventsRequest, srv api.Events_StreamServer) error {
	clientID := fmt.Sprintf("%d", time.Now().UnixNano())
	for {
		e := <-s.emitter.Events(srv.Context(), clientID)
		// upon the client event timeout this will be nil; ignore
		if e == nil {
			return nil
		}
		if err := srv.Send(e); err != nil {
			logrus.WithFields(logrus.Fields{
				"client": clientID,
			}).Debug("error sending event; unsubscribing client")
			s.emitter.Remove(clientID)
			return err
		}
	}
}
