package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/log"
	"github.com/nats-io/go-nats-streaming"
	"github.com/pkg/errors"
)

type natsPoster struct {
	sc stan.Conn
}

func NewNATSPoster(clusterID, clientID string) (Poster, error) {
	sc, err := stan.Connect(clusterID, clientID, stan.ConnectWait(5*time.Second))
	if err != nil {
		return nil, errors.Wrap(err, "failed to connect to nats streaming server")
	}
	return &natsPoster{sc}, nil

}

func (p *natsPoster) Post(ctx context.Context, e Event) {
	topic := getTopic(ctx)
	if topic == "" {
		log.G(ctx).WithField("event", e).Warn("unable to post event, topic is empty")
		return
	}

	data, err := json.Marshal(e)
	if err != nil {
		log.G(ctx).WithError(err).WithFields(logrus.Fields{"event": e, "topic": topic}).
			Warn("unable to marshal event")
		return
	}

	err = p.sc.Publish(topic, data)
	if err != nil {
		log.G(ctx).WithError(err).WithFields(logrus.Fields{"event": e, "topic": topic}).
			Warn("unable to post event")
	}

	log.G(ctx).WithFields(logrus.Fields{"event": e, "topic": topic}).
		Debug("Posted event")
}
