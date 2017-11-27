package events

import (
	"context"
	"time"

	"github.com/gogo/protobuf/types"
)

// Envelope provides the packaging for an event.
type Envelope struct {
	Timestamp time.Time
	Namespace string
	Topic     string
	Event     *types.Any
}

// Event is a generic interface for any type of event
type Event interface{}

// Publisher posts the event.
type Publisher interface {
	Publish(ctx context.Context, topic string, event Event) error
}

// Forwarder forwards an event to the underlying event bus
type Forwarder interface {
	Forward(ctx context.Context, envelope *Envelope) error
}

// Subscriber allows callers to subscribe to events
type Subscriber interface {
	Subscribe(ctx context.Context, filters ...string) (ch <-chan *Envelope, errs <-chan error)
}
