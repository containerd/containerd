/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package shim

import (
	"context"
	"net"
	"sync"
	"time"

	v1 "github.com/containerd/containerd/api/services/ttrpc/events/v1"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/typeurl"
	"github.com/sirupsen/logrus"
)

const (
	queueSize  = 2048
	maxRequeue = 5
)

type item struct {
	ev    *v1.Envelope
	ctx   context.Context
	count int
}

func newPublisher(address string) *remoteEventsPublisher {
	l := &remoteEventsPublisher{
		dialer: newDialier(func() (net.Conn, error) {
			return connect(address, dial)
		}),
		closed:  make(chan struct{}),
		requeue: make(chan *item, queueSize),
	}
	go l.processQueue()
	return l
}

type remoteEventsPublisher struct {
	dialer  *dialer
	closed  chan struct{}
	closer  sync.Once
	requeue chan *item
}

func (l *remoteEventsPublisher) Done() <-chan struct{} {
	return l.closed
}

func (l *remoteEventsPublisher) Close() (err error) {
	err = l.dialer.Close()
	l.closer.Do(func() {
		close(l.closed)
	})
	return err
}

func (l *remoteEventsPublisher) processQueue() {
	for i := range l.requeue {
		if i.count > maxRequeue {
			logrus.Errorf("evicting %s from queue because of retry count", i.ev.Topic)
			// drop the event
			continue
		}

		client, err := l.dialer.Get()
		if err != nil {
			l.dialer.Put(err)

			l.queue(i)
			logrus.WithError(err).Error("get events client")
			continue
		}
		if _, err := client.Forward(i.ctx, &v1.ForwardRequest{
			Envelope: i.ev,
		}); err != nil {
			l.dialer.Put(err)
			logrus.WithError(err).Error("forward event")
			l.queue(i)
		}
	}
}

func (l *remoteEventsPublisher) queue(i *item) {
	go func() {
		i.count++
		// re-queue after a short delay
		time.Sleep(time.Duration(1*i.count) * time.Second)
		l.requeue <- i
	}()
}

func (l *remoteEventsPublisher) Publish(ctx context.Context, topic string, event events.Event) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	any, err := typeurl.MarshalAny(event)
	if err != nil {
		return err
	}
	i := &item{
		ev: &v1.Envelope{
			Timestamp: time.Now(),
			Namespace: ns,
			Topic:     topic,
			Event:     any,
		},
		ctx: ctx,
	}
	client, err := l.dialer.Get()
	if err != nil {
		l.dialer.Put(err)
		l.queue(i)
		return err
	}
	if _, err := client.Forward(i.ctx, &v1.ForwardRequest{
		Envelope: i.ev,
	}); err != nil {
		l.dialer.Put(err)
		l.queue(i)
		return err
	}
	return nil
}

func connect(address string, d func(string, time.Duration) (net.Conn, error)) (net.Conn, error) {
	return d(address, 5*time.Second)
}
