package execution

import (
	"sync"

	"github.com/containerd/containerd/plugin"

	"golang.org/x/net/context"
)

func newCollector(ctx context.Context, runtimes map[string]plugin.Runtime) (*collector, error) {
	c := &collector{
		context:      ctx,
		ch:           make(chan *plugin.Event, 2048),
		eventClients: make(map[*eventClient]struct{}),
	}
	for _, r := range runtimes {
		if err := c.collect(r); err != nil {
			return nil, err
		}
	}
	// run the publisher
	go c.publisher()
	// run a goroutine that waits for the context to be done
	// and closes the channel after all writes have finished
	go c.waitDone()
	return c, nil
}

type eventClient struct {
	eCh chan error
	w   *grpcEventWriter
}

type collector struct {
	mu sync.Mutex
	wg sync.WaitGroup

	context      context.Context
	ch           chan *plugin.Event
	eventClients map[*eventClient]struct{}
}

// collect collects events from the provided runtime
func (c *collector) collect(r plugin.Runtime) error {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for e := range r.Events(c.context) {
			c.ch <- e
		}
	}()
	return nil
}

func (c *collector) forward(w *grpcEventWriter) error {
	client := &eventClient{
		w:   w,
		eCh: make(chan error, 1),
	}
	c.mu.Lock()
	c.eventClients[client] = struct{}{}
	c.mu.Unlock()
	err := <-client.eCh
	c.mu.Lock()
	delete(c.eventClients, client)
	c.mu.Unlock()
	return err
}

func (c *collector) publisher() {
	for e := range c.ch {
		c.mu.Lock()
		for client := range c.eventClients {
			if err := client.w.Write(e); err != nil {
				client.eCh <- err
			}
		}
		c.mu.Unlock()
	}
}

// waitDone waits for the context to finish, waits for all the goroutines to finish
// collecting grpc events from the shim, and closes the output channel
func (c *collector) waitDone() {
	<-c.context.Done()
	c.wg.Wait()
	close(c.ch)
}
