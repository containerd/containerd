package events

import (
	"context"
	"sync"
	"time"

	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/namespaces"
	goevents "github.com/docker/go-events"
)

const (
	EventVersion = "v1"
)

type Emitter struct {
	sinks       map[string]*goevents.RetryingSink
	broadcaster *goevents.Broadcaster
	m           sync.Mutex
}

func NewEmitter() *Emitter {
	return &Emitter{
		sinks:       make(map[string]*goevents.RetryingSink),
		broadcaster: goevents.NewBroadcaster(),
		m:           sync.Mutex{},
	}
}

func (e *Emitter) Post(ctx context.Context, evt Event) error {
	if err := e.broadcaster.Write(&sinkEvent{
		ctx:   ctx,
		event: evt,
	}); err != nil {
		return err
	}

	return nil
}

func (e *Emitter) Events(ctx context.Context, clientID string) chan *events.Envelope {
	e.m.Lock()
	defer e.m.Unlock()

	if v, ok := e.sinks[clientID]; ok {
		e.broadcaster.Remove(v)
	}

	ns, _ := namespaces.Namespace(ctx)
	s := &eventSink{
		id: clientID,
		ch: make(chan *events.Envelope),
		ns: ns,
	}
	q := goevents.NewQueue(s)
	r := goevents.NewRetryingSink(q, goevents.NewBreaker(5, time.Millisecond*100))

	e.broadcaster.Add(r)
	e.sinks[clientID] = r

	return s.ch
}

func (e *Emitter) Remove(clientID string) {
	e.m.Lock()
	if v, ok := e.sinks[clientID]; ok {
		e.broadcaster.Remove(v)
		delete(e.sinks, clientID)
	}
	e.m.Unlock()
}
