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

package sbserver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/containerd"
	eventtypes "github.com/containerd/containerd/api/events"
	containerdio "github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/pkg/cri/constants"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/pkg/cri/util"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/typeurl/v2"
	"github.com/sirupsen/logrus"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/utils/clock"
)

const (
	backOffInitDuration        = 1 * time.Second
	backOffMaxDuration         = 5 * time.Minute
	backOffExpireCheckDuration = 1 * time.Second

	// handleEventTimeout is the timeout for handling 1 event. Event monitor
	// handles events in serial, if one event blocks the event monitor, no
	// other events can be handled.
	// Add a timeout for each event handling, events that timeout will be requeued and
	// handled again in the future.
	handleEventTimeout = 10 * time.Second
)

// eventMonitor monitors containerd event and updates internal state correspondingly.
type eventMonitor struct {
	c       *criService
	ch      <-chan *events.Envelope
	errCh   <-chan error
	ctx     context.Context
	cancel  context.CancelFunc
	backOff *backOff
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

// Create new event monitor. New event monitor will start subscribing containerd event. All events
// happen after it should be monitored.
func newEventMonitor(c *criService) *eventMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &eventMonitor{
		c:       c,
		ctx:     ctx,
		cancel:  cancel,
		backOff: newBackOff(),
	}
}

// subscribe starts to subscribe containerd events.
func (em *eventMonitor) subscribe(subscriber events.Subscriber) {
	// note: filters are any match, if you want any match but not in namespace foo
	// then you have to manually filter namespace foo
	filters := []string{
		`topic=="/tasks/oom"`,
		`topic~="/images/"`,
	}
	em.ch, em.errCh = subscriber.Subscribe(em.ctx, filters...)
}

// startSandboxExitMonitor starts an exit monitor for a given sandbox.
func (em *eventMonitor) startSandboxExitMonitor(ctx context.Context, id string, pid uint32, exitCh <-chan containerd.ExitStatus) <-chan struct{} {
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		select {
		case exitRes := <-exitCh:
			exitStatus, exitedAt, err := exitRes.Result()
			if err != nil {
				logrus.WithError(err).Errorf("failed to get task exit status for %q", id)
				exitStatus = unknownExitCode
				exitedAt = time.Now()
			}

			e := &eventtypes.TaskExit{
				ContainerID: id,
				ID:          id,
				Pid:         pid,
				ExitStatus:  exitStatus,
				ExitedAt:    protobuf.ToTimestamp(exitedAt),
			}

			logrus.Debugf("received exit event %+v", e)

			err = func() error {
				dctx := ctrdutil.NamespacedContext()
				dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
				defer dcancel()

				sb, err := em.c.sandboxStore.Get(e.ID)
				if err == nil {
					if err := handleSandboxExit(dctx, e, sb, em.c); err != nil {
						return err
					}
					return nil
				} else if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to get sandbox %s: %w", e.ID, err)
				}
				return nil
			}()
			if err != nil {
				logrus.WithError(err).Errorf("failed to handle sandbox TaskExit event %+v", e)
				em.backOff.enBackOff(id, e)
			}
			return
		case <-ctx.Done():
		}
	}()
	return stopCh
}

// startContainerExitMonitor starts an exit monitor for a given container.
func (em *eventMonitor) startContainerExitMonitor(ctx context.Context, id string, pid uint32, exitCh <-chan containerd.ExitStatus) <-chan struct{} {
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		select {
		case exitRes := <-exitCh:
			exitStatus, exitedAt, err := exitRes.Result()
			if err != nil {
				logrus.WithError(err).Errorf("failed to get task exit status for %q", id)
				exitStatus = unknownExitCode
				exitedAt = time.Now()
			}

			e := &eventtypes.TaskExit{
				ContainerID: id,
				ID:          id,
				Pid:         pid,
				ExitStatus:  exitStatus,
				ExitedAt:    protobuf.ToTimestamp(exitedAt),
			}

			logrus.Debugf("received exit event %+v", e)

			err = func() error {
				dctx := ctrdutil.NamespacedContext()
				dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
				defer dcancel()

				cntr, err := em.c.containerStore.Get(e.ID)
				if err == nil {
					if err := handleContainerExit(dctx, e, cntr, cntr.SandboxID, em.c); err != nil {
						return err
					}
					return nil
				} else if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to get container %s: %w", e.ID, err)
				}
				return nil
			}()
			if err != nil {
				logrus.WithError(err).Errorf("failed to handle container TaskExit event %+v", e)
				em.backOff.enBackOff(id, e)
			}
			return
		case <-ctx.Done():
		}
	}()
	return stopCh
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
	case *eventtypes.ImageCreate:
		id = e.Name
	case *eventtypes.ImageUpdate:
		id = e.Name
	case *eventtypes.ImageDelete:
		id = e.Name
	default:
		return "", nil, errors.New("unsupported event")
	}
	return id, evt, nil
}

// start starts the event monitor which monitors and handles all subscribed events.
// It returns an error channel for the caller to wait for stop errors from the
// event monitor.
//
// NOTE:
//  1. start must be called after subscribe.
//  2. The task exit event has been handled in individual startSandboxExitMonitor
//     or startContainerExitMonitor goroutine at the first. If the goroutine fails,
//     it puts the event into backoff retry queue and event monitor will handle
//     it later.
func (em *eventMonitor) start() <-chan error {
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
				logrus.Debugf("Received containerd event timestamp - %v, namespace - %q, topic - %q", e.Timestamp, e.Namespace, e.Topic)
				if e.Namespace != constants.K8sContainerdNamespace {
					logrus.Debugf("Ignoring events in namespace - %q", e.Namespace)
					break
				}
				id, evt, err := convertEvent(e.Event)
				if err != nil {
					logrus.WithError(err).Errorf("Failed to convert event %+v", e)
					break
				}
				if em.backOff.isInBackOff(id) {
					logrus.Infof("Events for %q is in backoff, enqueue event %+v", id, evt)
					em.backOff.enBackOff(id, evt)
					break
				}
				if err := em.handleEvent(evt); err != nil {
					logrus.WithError(err).Errorf("Failed to handle event %+v for %s", evt, id)
					em.backOff.enBackOff(id, evt)
				}
			case err := <-em.errCh:
				// Close errCh in defer directly if there is no error.
				if err != nil {
					logrus.WithError(err).Error("Failed to handle event stream")
					errCh <- err
				}
				return
			case <-backOffCheckCh:
				ids := em.backOff.getExpiredIDs()
				for _, id := range ids {
					queue := em.backOff.deBackOff(id)
					for i, any := range queue.events {
						if err := em.handleEvent(any); err != nil {
							logrus.WithError(err).Errorf("Failed to handle backOff event %+v for %s", any, id)
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

// stop stops the event monitor. It will close the event channel.
// Once event monitor is stopped, it can't be started.
func (em *eventMonitor) stop() {
	em.backOff.stop()
	em.cancel()
}

// handleEvent handles a containerd event.
func (em *eventMonitor) handleEvent(any interface{}) error {
	ctx := ctrdutil.NamespacedContext()
	ctx, cancel := context.WithTimeout(ctx, handleEventTimeout)
	defer cancel()

	switch e := any.(type) {
	case *eventtypes.TaskExit:
		logrus.Infof("TaskExit event %+v", e)
		// Use ID instead of ContainerID to rule out TaskExit event for exec.
		cntr, err := em.c.containerStore.Get(e.ID)
		if err == nil {
			if err := handleContainerExit(ctx, e, cntr, cntr.SandboxID, em.c); err != nil {
				return fmt.Errorf("failed to handle container TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find container for TaskExit event: %w", err)
		}
		sb, err := em.c.sandboxStore.Get(e.ID)
		if err == nil {
			if err := handleSandboxExit(ctx, e, sb, em.c); err != nil {
				return fmt.Errorf("failed to handle sandbox TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find sandbox for TaskExit event: %w", err)
		}
		return nil
	case *eventtypes.TaskOOM:
		logrus.Infof("TaskOOM event %+v", e)
		// For TaskOOM, we only care which container it belongs to.
		cntr, err := em.c.containerStore.Get(e.ContainerID)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("can't find container for TaskOOM event: %w", err)
			}
			return nil
		}
		err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
			status.Reason = oomExitReason
			return status, nil
		})
		if err != nil {
			return fmt.Errorf("failed to update container status for TaskOOM event: %w", err)
		}
	case *eventtypes.ImageCreate:
		logrus.Infof("ImageCreate event %+v", e)
		return em.c.updateImage(ctx, e.Name)
	case *eventtypes.ImageUpdate:
		logrus.Infof("ImageUpdate event %+v", e)
		return em.c.updateImage(ctx, e.Name)
	case *eventtypes.ImageDelete:
		logrus.Infof("ImageDelete event %+v", e)
		return em.c.updateImage(ctx, e.Name)
	}

	return nil
}

// handleContainerExit handles TaskExit event for container.
func handleContainerExit(ctx context.Context, e *eventtypes.TaskExit, cntr containerstore.Container, sandboxID string, c *criService) error {
	// Attach container IO so that `Delete` could cleanup the stream properly.
	task, err := cntr.Container.Task(ctx,
		func(*containerdio.FIFOSet) (containerdio.IO, error) {
			// We can't directly return cntr.IO here, because
			// even if cntr.IO is nil, the cio.IO interface
			// is not.
			// See https://tour.golang.org/methods/12:
			//   Note that an interface value that holds a nil
			//   concrete value is itself non-nil.
			if cntr.IO != nil {
				return cntr.IO, nil
			}
			return nil, nil
		},
	)
	if err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to load task for container: %w", err)
		}
	} else {
		// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
		if _, err = task.Delete(ctx, c.nri.WithContainerExit(&cntr), containerd.WithProcessKill); err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to stop container: %w", err)
			}
			// Move on to make sure container status is updated.
		}
	}
	err = cntr.Status.UpdateSync(func(status containerstore.Status) (containerstore.Status, error) {
		if status.FinishedAt == 0 {
			status.Pid = 0
			status.FinishedAt = protobuf.FromTimestamp(e.ExitedAt).UnixNano()
			status.ExitCode = int32(e.ExitStatus)
		}

		// Unknown state can only transit to EXITED state, so we need
		// to handle unknown state here.
		if status.Unknown {
			logrus.Debugf("Container %q transited from UNKNOWN to EXITED", cntr.ID)
			status.Unknown = false
		}
		return status, nil
	})
	if err != nil {
		return fmt.Errorf("failed to update container state: %w", err)
	}
	// Using channel to propagate the information of container stop
	cntr.Stop()
	c.generateAndSendContainerEvent(ctx, cntr.ID, sandboxID, runtime.ContainerEventType_CONTAINER_STOPPED_EVENT)
	return nil
}

// handleSandboxExit handles TaskExit event for sandbox.
func handleSandboxExit(ctx context.Context, e *eventtypes.TaskExit, sb sandboxstore.Sandbox, c *criService) error {
	// TODO: Move pause container cleanup to podsandbox/ package.
	if sb.Container != nil {
		// No stream attached to sandbox container.
		task, err := sb.Container.Task(ctx, nil)
		if err != nil {
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to load task for sandbox: %w", err)
			}
		} else {
			// TODO(random-liu): [P1] This may block the loop, we may want to spawn a worker
			if _, err = task.Delete(ctx, containerd.WithProcessKill); err != nil {
				if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to stop sandbox: %w", err)
				}
				// Move on to make sure container status is updated.
			}
		}
	}

	if err := sb.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		status.State = sandboxstore.StateNotReady
		status.Pid = 0
		return status, nil
	}); err != nil {
		return fmt.Errorf("failed to update sandbox state: %w", err)
	}

	// Using channel to propagate the information of sandbox stop
	sb.Stop()
	c.generateAndSendContainerEvent(ctx, sb.ID, sb.ID, runtime.ContainerEventType_CONTAINER_STOPPED_EVENT)
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

// enBackOff get out the whole queue
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

	duration := 2 * oldDuration
	if duration > b.maxDuration {
		duration = b.maxDuration
	}
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
