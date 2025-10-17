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

package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"

	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/internal/cri/clock"
	"github.com/containerd/containerd/v2/internal/cri/constants"
)

const (
	backOffInitDuration        = 1 * time.Second
	backOffMaxDuration         = 5 * time.Minute
	backOffExpireCheckDuration = 1 * time.Second
)

type EventHandler interface {
	HandleEvent(any interface{}) error
}

// EventMonitor monitors containerd event and updates internal state correspondingly.
type EventMonitor struct {
	ch           <-chan *events.Envelope
	errCh        <-chan error
	ctx          context.Context
	cancel       context.CancelFunc
	backOff      *backOff
	eventHandler EventHandler
}

type backOff struct {
	// queuePoolMu is mutex used to protect the queuePool map
	queuePoolMu sync.Mutex

	queuePool map[string]*backOffQueue
	// tickerMu is mutex used to protect the ticker.
	tickerMu      sync.Mutex
	ticker        *time.Ticker
	minDuration   time.Duration
	maxDuration   time.Duration
	checkDuration time.Duration
	clock         clock.Clock
}

type backOffQueue struct {
	events     []interface{}
	expireTime time.Time
	duration   time.Duration
	clock      clock.Clock
}

// NewEventMonitor create new event monitor. New event monitor will Start subscribing containerd event. All events
// happen after it should be monitored.
func NewEventMonitor(eventHandler EventHandler) *EventMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &EventMonitor{
		ctx:          ctx,
		cancel:       cancel,
		backOff:      newBackOff(),
		eventHandler: eventHandler,
	}
}

// Subscribe starts to Subscribe containerd events.
func (em *EventMonitor) Subscribe(subscriber events.Subscriber, filters []string) {
	em.ch, em.errCh = subscriber.Subscribe(em.ctx, filters...)
}

func convertEvent(e typeurl.Any) (string, interface{}, error) {
	id := ""
	evt, err := typeurl.UnmarshalAny(e)
	if err != nil {
		return "", nil, fmt.Errorf("failed to unmarshalany: %w", err)
	}

	switch e := evt.(type) {
	case *eventtypes.TaskOOM:
		id = e.ContainerID
	case *eventtypes.SandboxExit:
		id = e.SandboxID
	case *eventtypes.ImageCreate:
		id = e.Name
	case *eventtypes.ImageUpdate:
		id = e.Name
	case *eventtypes.ImageDelete:
		id = e.Name
	case *eventtypes.TaskExit:
		id = e.ContainerID
	default:
		return "", nil, errors.New("unsupported event")
	}
	return id, evt, nil
}

// Start starts the event monitor which monitors and handles all subscribed events.
// It returns an error channel for the caller to wait for Stop errors from the
// event monitor.
//
// NOTE:
//  1. Start must be called after Subscribe.
//  2. The task exit event has been handled in individual startSandboxExitMonitor
//     or startContainerExitMonitor goroutine at the first. If the goroutine fails,
//     it puts the event into backoff retry queue and event monitor will handle
//     it later.
func (em *EventMonitor) Start() <-chan error {
	errCh := make(chan error)
	if em.ch == nil || em.errCh == nil {
		panic("event channel is nil")
	}
	backOffCheckCh := em.backOff.start()
	go func() {
		defer close(errCh)
		for {
			select {
			case e := <-em.ch:
				log.L.Debugf("Received containerd event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
				if e.Namespace != constants.K8sContainerdNamespace {
					log.L.Debugf("Ignoring events in namespace - %q", e.Namespace)
					break
				}
				id, evt, err := convertEvent(e.Event)
				if err != nil {
					log.L.WithError(err).Errorf("Failed to convert event %+v", e)
					break
				}
				if em.backOff.isInBackOff(id) {
					log.L.Infof("Events for %q is in backoff, enqueue event %+v", id, evt)
					em.backOff.enBackOff(id, evt)
					break
				}
				if err := em.eventHandler.HandleEvent(evt); err != nil {
					log.L.WithError(err).Errorf("Failed to handle event %+v for %s", evt, id)
					em.backOff.enBackOff(id, evt)
				}
			case err := <-em.errCh:
				// Close errCh in defer directly if there is no error.
				if err != nil {
					log.L.WithError(err).Error("Failed to handle event stream")
					errCh <- err
				}
				return
			case <-backOffCheckCh:
				ids := em.backOff.getExpiredIDs()
				for _, id := range ids {
					queue := em.backOff.deBackOff(id)
					for i, evt := range queue.events {
						if err := em.eventHandler.HandleEvent(evt); err != nil {
							log.L.WithError(err).Errorf("Failed to handle backOff event %+v for %s", evt, id)
							em.backOff.reBackOff(id, queue.events[i:], queue.duration)
							break
						}
					}
				}
			}
		}
	}()
	return errCh
}

func (em *EventMonitor) Backoff(key string, evt interface{}) {
	em.backOff.enBackOff(key, evt)
}

// Stop stops the event monitor. It will close the event channel.
// Once event monitor is stopped, it can't be started.
func (em *EventMonitor) Stop() {
	em.backOff.stop()
	em.cancel()
}

func newBackOff() *backOff {
	return &backOff{
		queuePool:     map[string]*backOffQueue{},
		minDuration:   backOffInitDuration,
		maxDuration:   backOffMaxDuration,
		checkDuration: backOffExpireCheckDuration,
		clock:         clock.RealClock{},
	}
}

func (b *backOff) getExpiredIDs() []string {
	b.queuePoolMu.Lock()
	defer b.queuePoolMu.Unlock()

	var ids []string
	for id, q := range b.queuePool {
		if q.isExpire() {
			ids = append(ids, id)
		}
	}
	return ids
}

func (b *backOff) isInBackOff(key string) bool {
	b.queuePoolMu.Lock()
	defer b.queuePoolMu.Unlock()

	if _, ok := b.queuePool[key]; ok {
		return true
	}
	return false
}

// enBackOff start to backOff and put event to the tail of queue
func (b *backOff) enBackOff(key string, evt interface{}) {
	b.queuePoolMu.Lock()
	defer b.queuePoolMu.Unlock()

	if queue, ok := b.queuePool[key]; ok {
		queue.events = append(queue.events, evt)
		return
	}
	b.queuePool[key] = newBackOffQueue([]interface{}{evt}, b.minDuration, b.clock)
}

// deBackOff get out the whole queue
func (b *backOff) deBackOff(key string) *backOffQueue {
	b.queuePoolMu.Lock()
	defer b.queuePoolMu.Unlock()

	queue := b.queuePool[key]
	delete(b.queuePool, key)
	return queue
}

// enBackOff start to backOff again and put events to the queue
func (b *backOff) reBackOff(key string, events []interface{}, oldDuration time.Duration) {
	b.queuePoolMu.Lock()
	defer b.queuePoolMu.Unlock()

	duration := min(2*oldDuration, b.maxDuration)
	b.queuePool[key] = newBackOffQueue(events, duration, b.clock)
}

func (b *backOff) start() <-chan time.Time {
	b.tickerMu.Lock()
	defer b.tickerMu.Unlock()
	b.ticker = time.NewTicker(b.checkDuration)
	return b.ticker.C
}

func (b *backOff) stop() {
	b.tickerMu.Lock()
	defer b.tickerMu.Unlock()
	if b.ticker != nil {
		b.ticker.Stop()
	}
}

func newBackOffQueue(events []interface{}, init time.Duration, c clock.Clock) *backOffQueue {
	return &backOffQueue{
		events:     events,
		duration:   init,
		expireTime: c.Now().Add(init),
		clock:      c,
	}
}

func (q *backOffQueue) isExpire() bool {
	// return time.Now >= expireTime
	return !q.clock.Now().Before(q.expireTime)
}
