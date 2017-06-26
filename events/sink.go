package events

import (
	"context"
	"reflect"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/log"
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
	ch chan *events.Envelope
	f  filters.Filter
}

func (s sinkEvent) Field(fieldpath []string) (string, bool) {
	if len(fieldpath) == 0 {
		return "", false
	}
	topic := getTopic(s.ctx)
	switch fieldpath[0] {
	case "topic":
		return topic, len(topic) > 0
	}

	return "", false
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

	topic := getTopic(e.ctx)

	// filter
	if !s.f.Match(e) {
		var f filters.Adaptor
		switch v := e.event.(type) {
		case *events.ContainerCreate:
			f = v
		case *events.ContainerUpdate:
			f = v
		case *events.ContainerDelete:
			f = v
			// TODO (ehazlett): other event types
		}

		if f == nil || !s.f.Match(f) {
			return nil
		}
	}

	eventData, err := MarshalEvent(e.event)
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

func adaptEvent(topic string, evt Event) filters.Adaptor {
	return filters.AdapterFunc(func(fieldpath []string) (string, bool) {
		if len(fieldpath) == 0 {
			return "", false
		}

		s := evt.(*sinkEvent)

		switch fieldpath[0] {
		case "container_id":
			switch v := s.event.(type) {
			case *events.ContainerCreate:
				return v.ContainerID, len(v.ContainerID) > 0
			case *events.ContainerUpdate:
				return v.ContainerID, len(v.ContainerID) > 0
			case *events.ContainerDelete:
				return v.ContainerID, len(v.ContainerID) > 0
			case *events.TaskCreate:
				return v.ContainerID, len(v.ContainerID) > 0
			case *events.TaskStart:
				return v.ContainerID, len(v.ContainerID) > 0
			case *events.TaskDelete:
				return v.ContainerID, len(v.ContainerID) > 0
			default:
				return "", false
			}
		case "image":
			switch v := s.event.(type) {
			case *events.ImageUpdate:
				return v.Name, len(v.Name) > 0
			case *events.ImageDelete:
				return v.Name, len(v.Name) > 0
			default:
				return "", false
			}
		case "topic":
			return topic, len(topic) > 0
		case "type":
			k := reflect.TypeOf(s.event)
			v := k.Elem().Name()
			return v, true
		}

		return "", false
	})
}
