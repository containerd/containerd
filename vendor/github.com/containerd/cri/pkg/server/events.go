/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	"time"

	eventtypes "github.com/containerd/containerd/api/events"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/typeurl"
	gogotypes "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/util/clock"

	ctrdutil "github.com/containerd/cri/pkg/containerd/util"
	"github.com/containerd/cri/pkg/store"
	containerstore "github.com/containerd/cri/pkg/store/container"
	sandboxstore "github.com/containerd/cri/pkg/store/sandbox"
)

const (
	backOffInitDuration        = 1 * time.Second
	backOffMaxDuration         = 5 * time.Minute
	backOffExpireCheckDuration = 1 * time.Second
)

// eventMonitor monitors containerd event and updates internal state correspondingly.
// TODO(random-liu): [P1] Figure out is it possible to drop event during containerd
// is running. If it is, we should do periodically list to sync state with containerd.
type eventMonitor struct {
	containerStore *containerstore.Store
	sandboxStore   *sandboxstore.Store
	ch             <-chan *events.Envelope
	errCh          <-chan error
	ctx            context.Context
	cancel         context.CancelFunc
	backOff        *backOff
}

type backOff struct {
	queuePool     map[string]*backOffQueue
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

// Create new event monitor. New event monitor will start subscribing containerd event. All events
// happen after it should be monitored.
func newEventMonitor(c *containerstore.Store, s *sandboxstore.Store) *eventMonitor {
	// event subscribe doesn't need namespace.
	ctx, cancel := context.WithCancel(context.Background())
	return &eventMonitor{
		containerStore: c,
		sandboxStore:   s,
		ctx:            ctx,
		cancel:         cancel,
		backOff:        newBackOff(),
	}
}

// subscribe starts to subscribe containerd events.
func (em *eventMonitor) subscribe(subscriber events.Subscriber) {
	filters := []string{
		`topic=="/tasks/exit"`,
		`topic=="/tasks/oom"`,
	}
	em.ch, em.errCh = subscriber.Subscribe(em.ctx, filters...)
}

func convertEvent(e *gogotypes.Any) (string, interface{}, error) {
	containerID := ""
	evt, err := typeurl.UnmarshalAny(e)
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to unmarshalany")
	}

	switch evt.(type) {
	case *eventtypes.TaskExit:
		containerID = evt.(*eventtypes.TaskExit).ContainerID
	case *eventtypes.TaskOOM:
		containerID = evt.(*eventtypes.TaskOOM).ContainerID
	default:
		return "", nil, errors.New("unsupported event")
	}
	return containerID, evt, nil
}

// start starts the event monitor which monitors and handles all container events. It returns
// a channel for the caller to wait for the event monitor to stop. start must be called after
// subscribe.
func (em *eventMonitor) start() (<-chan struct{}, error) {
	if em.ch == nil || em.errCh == nil {
		return nil, errors.New("event channel is nil")
	}
	closeCh := make(chan struct{})
	go func() {
		backOffCheckCh := em.backOff.start()
		for {
			select {
			case e := <-em.ch:
				logrus.Debugf("Received containerd event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
				cID, evt, err := convertEvent(e.Event)
				if err != nil {
					logrus.WithError(err).Errorf("Failed to convert event %+v", e)
					break
				}
				if em.backOff.isInBackOff(cID) {
					logrus.Infof("Events for container %q is in backoff, enqueue event %+v", cID, evt)
					em.backOff.enBackOff(cID, evt)
					break
				}
				if err := em.handleEvent(evt); err != nil {
					logrus.WithError(err).Errorf("Failed to handle event %+v for container %s", evt, cID)
					em.backOff.enBackOff(cID, evt)
				}
			case err := <-em.errCh:
				logrus.WithError(err).Error("Failed to handle event stream")
				close(closeCh)
				return
			case <-backOffCheckCh:
				cIDs := em.backOff.getExpiredContainers()
				for _, cID := range cIDs {
					queue := em.backOff.deBackOff(cID)
					for i, any := range queue.events {
						if err := em.handleEvent(any); err != nil {
							logrus.WithError(err).Errorf("Failed to handle backOff event %+v for container %s", any, cID)
							em.backOff.reBackOff(cID, queue.events[i:], queue.duration)
							break
						}
					}
				}
			}
		}
	}()
	return closeCh, nil
}

// stop stops the event monitor. It will close the event channel.
// Once event monitor is stopped, it can't be started.
func (em *eventMonitor) stop() {
	em.backOff.stop()
	em.cancel()
}

// handleEvent handles a containerd event.
func (em *eventMonitor) handleEvent(any interface{}) error {
	ctx := ctrdutil.NamespacedContext()
	switch any.(type) {
	// If containerd-shim exits unexpectedly, there will be no corresponding event.
	// However, containerd could not retrieve container state in that case, so it's
	// fine to leave out that case for now.
	// TODO(random-liu): [P2] Handle containerd-shim exit.
	case *eventtypes.TaskExit:
		e := any.(*eventtypes.TaskExit)
		cntr, err := em.containerStore.Get(e.ContainerID)
		if err == nil {
			if err := handleContainerExit(ctx, e, cntr); err != nil {
				return errors.Wrap(err, "failed to handle container TaskExit event")
			}
			return nil
		} else if err != store.ErrNotExist {
			return errors.Wrap(err, "can't find container for TaskExit event")
		}
		// Use GetAll to include sandbox in unknown state.
		sb, err := em.sandboxStore.GetAll(e.ContainerID)
		if err == nil {
			if err := handleSandboxExit(ctx, e, sb); err != nil {
				return errors.Wrap(err, "failed to handle sandbox TaskExit event")
			}
			return nil
		} else if err != store.ErrNotExist {
			return errors.Wrap(err, "can't find sandbox for TaskExit event")
		}
		return nil
	case *eventtypes.TaskOOM:
		e := any.(*eventtypes.TaskOOM)
		logrus.Infof("TaskOOM event %+v", e)
		cntr, err := em.containerStore.Get(e.ContainerID)
		if err != nil {
			if err != store.ErrNotExist {
				return errors.Wrap(err, "can't find container for TaskOOM event")
			}
			if _, err = em.sandboxStore.Get(e.ContainerID); err != nil {
				if err != store.ErrNotExist {
					return errors.Wrap(err, "can't find sandbox for TaskOOM event")
				}
				return nil
			}
			return nil
		}
		err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			return errors.Wrap(err, "failed to update container status for TaskOOM event")
		}
	}

	return nil
}

// handleContainerExit handles TaskExit event for container.
func handleContainerExit(ctx context.Context, e *eventtypes.TaskExit, cntr containerstore.Container) error {
	if e.Pid != cntr.Status.Get().Pid {
		// Non-init process died, ignore the event.
		return nil
	}
	// Attach container IO so that `Delete` could cleanup the stream properly.
	task, err := cntr.Container.Task(ctx,
		func(*containerdio.FIFOSet) (containerdio.IO, error) {
			return cntr.IO, nil
		},
	)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return errors.Wrapf(err, "failed to load task for container")
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx); err != nil {
			if !errdefs.IsNotFound(err) {
				return errors.Wrap(err, "failed to stop container")
			}
			// Move on to make sure container status is updated.
		}
	}
	err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
		// If FinishedAt has been set (e.g. with start failure), keep as
		// it is.
		if status.FinishedAt != 0 {
			return status, nil
		}
		status.Pid = 0
		status.FinishedAt = e.ExitedAt.UnixNano()
		status.ExitCode = int32(e.ExitStatus)
		return status, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to update container state")
	}
	// Using channel to propagate the information of container stop
	cntr.Stop()
	return nil
}

// handleSandboxExit handles TaskExit event for sandbox.
func handleSandboxExit(ctx context.Context, e *eventtypes.TaskExit, sb sandboxstore.Sandbox) error {
	if e.Pid != sb.Status.Get().Pid {
		// Non-init process died, ignore the event.
		return nil
	}
	// No stream attached to sandbox container.
	task, err := sb.Container.Task(ctx, nil)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return errors.Wrap(err, "failed to load task for sandbox")
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx); err != nil {
			if !errdefs.IsNotFound(err) {
				return errors.Wrap(err, "failed to stop sandbox")
			}
			// Move on to make sure container status is updated.
		}
	}
	err = sb.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		// NOTE(random-liu): We SHOULD NOT change UNKNOWN state here.
		// If sandbox state is UNKNOWN when event monitor receives an TaskExit event,
		// it means that sandbox start has failed. In that case, `RunPodSandbox` will
		// cleanup everything immediately.
		// Once sandbox state goes out of UNKNOWN, it becomes visable to the user, which
		// is not what we want.
		if status.State != sandboxstore.StateUnknown {
			status.State = sandboxstore.StateNotReady
		}
		status.Pid = 0
		return status, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to update sandbox state")
	}
	// Using channel to propagate the information of sandbox stop
	sb.Stop()
	return nil
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

func (b *backOff) getExpiredContainers() []string {
	var containers []string
	for c, q := range b.queuePool {
		if q.isExpire() {
			containers = append(containers, c)
		}
	}
	return containers
}

func (b *backOff) isInBackOff(key string) bool {
	if _, ok := b.queuePool[key]; ok {
		return true
	}
	return false
}

// enBackOff start to backOff and put event to the tail of queue
func (b *backOff) enBackOff(key string, evt interface{}) {
	if queue, ok := b.queuePool[key]; ok {
		queue.events = append(queue.events, evt)
		return
	}
	b.queuePool[key] = newBackOffQueue([]interface{}{evt}, b.minDuration, b.clock)
}

// enBackOff get out the whole queue
func (b *backOff) deBackOff(key string) *backOffQueue {
	queue := b.queuePool[key]
	delete(b.queuePool, key)
	return queue
}

// enBackOff start to backOff again and put events to the queue
func (b *backOff) reBackOff(key string, events []interface{}, oldDuration time.Duration) {
	duration := 2 * oldDuration
	if duration > b.maxDuration {
		duration = b.maxDuration
	}
	b.queuePool[key] = newBackOffQueue(events, duration, b.clock)
}

func (b *backOff) start() <-chan time.Time {
	b.ticker = time.NewTicker(b.checkDuration)
	return b.ticker.C
}

func (b *backOff) stop() {
	b.ticker.Stop()
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
