package events

import (
	"context"
	"time"

	"github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/typeurl"
	goevents "github.com/docker/go-events"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type sinkEvent struct {
	ctx   context.Context
	event Event
}

type eventSink struct {
	ns string
	ch chan *events.Envelope
}

func (s *eventSink) Write(evt goevents.Event) error {
	e, ok := evt.(*sinkEvent)
	if !ok {
		return errors.New("event is not a sink event")
	}

	ns, _ := namespaces.Namespace(e.ctx)
	if ns != "" && ns != s.ns {
		// ignore events not intended for this ns
		return nil
	}

	if ev, ok := e.event.(*events.Envelope); ok {
		s.ch <- ev
		return nil
	}
	topic := getTopic(e.ctx)

	eventData, err := typeurl.MarshalAny(e.event)
	if err != nil {
		return err
	}

	log.G(e.ctx).WithFields(logrus.Fields{
		"topic": topic,
		"type":  eventData.TypeUrl,
		"ns":    ns,
	}).Debug("event")

	s.ch <- &events.Envelope{
		Timestamp: time.Now(),
		Topic:     topic,
		Event:     eventData,
	}
	return nil
}

func (s *eventSink) Close() error {
	return nil
}
