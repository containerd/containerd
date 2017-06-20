package events

import (
	"context"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/api/types/event"
	"github.com/containerd/containerd/namespaces"
	goevents "github.com/docker/go-events"
	"github.com/pkg/errors"
)

type sinkEvent struct {
	ctx   context.Context
	event Event
}

type eventSink struct {
	ns string
	ch chan *event.Envelope
}

func (s *eventSink) Write(evt goevents.Event) error {
	e, ok := evt.(*sinkEvent)
	if !ok {
		return errors.New("event is not a sink event")
	}
	topic := getTopic(e.ctx)

	ns, _ := namespaces.Namespace(e.ctx)
	if ns != "" && ns != s.ns {
		// ignore events not intended for this ns
		return nil
	}

	eventData, err := convertToAny(e.event)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"topic": topic,
		"type":  eventData.TypeUrl,
		"ns":    ns,
	}).Debug("event")

	s.ch <- &event.Envelope{
		Timestamp: time.Now(),
		Topic:     topic,
		Event:     eventData,
	}
	return nil
}

func (s *eventSink) Close() error {
	return nil
}
