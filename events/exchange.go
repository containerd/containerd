package events

import (
	"context"
	"strings"
	"time"

	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/typeurl"
	goevents "github.com/docker/go-events"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Exchange struct {
	broadcaster *goevents.Broadcaster
}

func NewExchange() *Exchange {
	return &Exchange{
		broadcaster: goevents.NewBroadcaster(),
	}
}

// Forward accepts an envelope to be direcly distributed on the exchange.
func (e *Exchange) Forward(ctx context.Context, envelope *events.Envelope) error {
	log.G(ctx).WithFields(logrus.Fields{
		"topic": envelope.Topic,
		"ns":    envelope.Namespace,
		"type":  envelope.Event.TypeUrl,
	}).Debug("forward event")

	if err := namespaces.Validate(envelope.Namespace); err != nil {
		return errors.Wrapf(err, "event envelope has invalid namespace")
	}

	if err := validateTopic(envelope.Topic); err != nil {
		return errors.Wrapf(err, "envelope topic %q", envelope.Topic)
	}

	return e.broadcaster.Write(envelope)
}

// Publish packages and sends an event. The caller will be considered the
// initial publisher of the event. This means the timestamp will be calculated
// at this point and this method may read from the calling context.
func (e *Exchange) Publish(ctx context.Context, topic string, event Event) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return errors.Wrapf(err, "failed publishing event")
	}
	if err := validateTopic(topic); err != nil {
		return errors.Wrapf(err, "envelope topic %q", topic)
	}

	evany, err := typeurl.MarshalAny(event)
	if err != nil {
		return err
	}
	env := events.Envelope{
		Timestamp: time.Now().UTC(),
		Topic:     topic,
		Event:     evany,
	}
	if err := e.broadcaster.Write(&env); err != nil {
		return err
	}

	log.G(ctx).WithFields(logrus.Fields{
		"topic": topic,
		"type":  evany.TypeUrl,
		"ns":    namespace,
	}).Debug("published event")
	return nil
}

// Subscribe to events on the exchange. Events are sent through the returned
// channel ch. If an error is encountered, it will be sent on channel errs and
// errs will be closed. To end the subscription, cancel the provided context.
func (e *Exchange) Subscribe(ctx context.Context, filters ...filters.Filter) (ch <-chan *events.Envelope, errs <-chan error) {
	var (
		evch    = make(chan *events.Envelope)
		errq    = make(chan error, 1)
		channel = goevents.NewChannel(0)
		queue   = goevents.NewQueue(channel)
	)

	// TODO(stevvooe): Insert the filter!

	e.broadcaster.Add(queue)

	go func() {
		defer close(errq)
		defer e.broadcaster.Remove(queue)
		defer queue.Close()

		var err error
	loop:
		for {
			select {
			case ev := <-channel.C:
				env, ok := ev.(*events.Envelope)
				if !ok {
					// TODO(stevvooe): For the most part, we are well protected
					// from this condition. Both Forward and Publish protect
					// from this.
					err = errors.Errorf("invalid envelope encountered %#v; please file a bug", ev)
					break
				}

				select {
				case evch <- env:
				case <-ctx.Done():
					break loop
				}
			case <-ctx.Done():
				break loop
			}
		}

		if err == nil {
			if cerr := ctx.Err(); cerr != context.Canceled {
				err = cerr
			}
		}

		errq <- err
	}()

	ch = evch
	errs = errq

	return
}

func validateTopic(topic string) error {
	if topic == "" {
		return errors.Wrap(errdefs.ErrInvalidArgument, "must not be empty")
	}

	if topic[0] != '/' {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "must start with '/'", topic)
	}

	if len(topic) == 1 {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "must have at least one component", topic)
	}

	components := strings.Split(topic[1:], "/")
	for _, component := range components {
		if err := identifiers.Validate(component); err != nil {
			return errors.Wrapf(err, "failed validation on component %q", component)
		}
	}

	return nil
}
