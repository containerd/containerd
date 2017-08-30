package events

import (
	"context"

	events "github.com/containerd/containerd/api/services/events/v1"
)

type Event interface{}

// Publisher posts the event.
type Publisher interface {
	Publish(ctx context.Context, topic string, event Event) error
}

type Forwarder interface {
	Forward(ctx context.Context, envelope *events.Envelope) error
}

type publisherFunc func(ctx context.Context, topic string, event Event) error

func (fn publisherFunc) Publish(ctx context.Context, topic string, event Event) error {
	return fn(ctx, topic, event)
}

type Subscriber interface {
	Subscribe(ctx context.Context, filters ...string) (ch <-chan *events.Envelope, errs <-chan error)
}
