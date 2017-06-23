package events

import (
	"context"
	"sync"

	events "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/namespaces"
	goevents "github.com/docker/go-events"
)

const (
	EventVersion = "v1"
)

type Emitter struct {
	sinks       map[string]*eventSink
	broadcaster *goevents.Broadcaster
	m           sync.Mutex
}

func NewEmitter() *Emitter {
	return &Emitter{
		sinks:       make(map[string]*eventSink),
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
	if _, ok := e.sinks[clientID]; !ok {
		ns, _ := namespaces.Namespace(ctx)
		s := &eventSink{
			ch: make(chan *events.Envelope),
			ns: ns,
		}
		e.sinks[clientID] = s
		e.broadcaster.Add(s)
	}
	ch := e.sinks[clientID].ch
	e.m.Unlock()

	return ch
}

func (e *Emitter) Remove(clientID string) {
	e.m.Lock()
	if v, ok := e.sinks[clientID]; ok {
		e.broadcaster.Remove(v)
		delete(e.sinks, clientID)
	}
	e.m.Unlock()
}
