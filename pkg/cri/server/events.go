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

package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/utils/clock"

	eventtypes "github.com/containerd/containerd/v2/api/events"
	apitasks "github.com/containerd/containerd/v2/api/services/tasks/v1"
	containerdio "github.com/containerd/containerd/v2/cio"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/events"
	"github.com/containerd/containerd/v2/pkg/cri/constants"
	containerstore "github.com/containerd/containerd/v2/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/pkg/cri/store/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/pkg/cri/util"
	"github.com/containerd/containerd/v2/protobuf"
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
func (em *eventMonitor) startSandboxExitMonitor(ctx context.Context, id string, exitCh <-chan containerd.ExitStatus) <-chan struct{} {
	stopCh := make(chan struct{})
	go func() {
		defer close(stopCh)
		select {
		case exitRes := <-exitCh:
			exitStatus, exitedAt, err := exitRes.Result()
			if err != nil {
				log.L.WithError(err).Errorf("failed to get task exit status for %q", id)
				exitStatus = unknownExitCode
				exitedAt = time.Now()
			}

			e := &eventtypes.SandboxExit{
				SandboxID:  id,
				ExitStatus: exitStatus,
				ExitedAt:   protobuf.ToTimestamp(exitedAt),
			}

			log.L.Debugf("received exit event %+v", e)

			err = func() error {
				dctx := ctrdutil.NamespacedContext()
				dctx, dcancel := context.WithTimeout(dctx, handleEventTimeout)
				defer dcancel()

				sb, err := em.c.sandboxStore.Get(e.GetSandboxID())
				if err == nil {
					if err := handleSandboxExit(dctx, sb, e.ExitStatus, e.ExitedAt.AsTime(), em.c); err != nil {
						return err
					}
					return nil
				} else if !errdefs.IsNotFound(err) {
					return fmt.Errorf("failed to get sandbox %s: %w", e.SandboxID, err)
				}
				return nil
			}()
			if err != nil {
				log.L.WithError(err).Errorf("failed to handle sandbox TaskExit event %+v", e)
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
				log.L.WithError(err).Errorf("failed to get task exit status for %q", id)
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

			log.L.Debugf("received exit event %+v", e)

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
				log.L.WithError(err).Errorf("failed to handle container TaskExit event %+v", e)
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
	case *eventtypes.SandboxExit:
		id = e.SandboxID
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
				if err := em.handleEvent(evt); err != nil {
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
						if err := em.handleEvent(evt); err != nil {
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
		log.L.Infof("TaskExit event %+v", e)
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
			if err := handleSandboxExit(ctx, sb, e.ExitStatus, e.ExitedAt.AsTime(), em.c); err != nil {
				return fmt.Errorf("failed to handle sandbox TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find sandbox for TaskExit event: %w", err)
		}
		return nil
	case *eventtypes.SandboxExit:
		log.L.Infof("SandboxExit event %+v", e)
		sb, err := em.c.sandboxStore.Get(e.GetSandboxID())
		if err == nil {
			if err := handleSandboxExit(ctx, sb, e.ExitStatus, e.ExitedAt.AsTime(), em.c); err != nil {
				return fmt.Errorf("failed to handle sandbox TaskExit event: %w", err)
			}
			return nil
		} else if !errdefs.IsNotFound(err) {
			return fmt.Errorf("can't find sandbox for TaskExit event: %w", err)
		}
		return nil
	case *eventtypes.TaskOOM:
		log.L.Infof("TaskOOM event %+v", e)
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
	// TODO: ImageService should handle these events directly
	case *eventtypes.ImageCreate:
		log.L.Infof("ImageCreate event %+v", e)
		return em.c.UpdateImage(ctx, e.Name)
	case *eventtypes.ImageUpdate:
		log.L.Infof("ImageUpdate event %+v", e)
		return em.c.UpdateImage(ctx, e.Name)
	case *eventtypes.ImageDelete:
		log.L.Infof("ImageDelete event %+v", e)
		return em.c.UpdateImage(ctx, e.Name)
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
		if !errdefs.IsNotFound(err) && !errdefs.IsUnavailable(err) {
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

	// NOTE: Both sb.Container.Task and task.Delete interface always ensures
	// that the status of target task. However, the interfaces return
	// ErrNotFound, which doesn't mean that the shim instance doesn't exist.
	//
	// There are two caches for task in containerd:
	//
	//   1. io.containerd.service.v1.tasks-service
	//   2. io.containerd.runtime.v2.task
	//
	// First one is to maintain the shim connection and shutdown the shim
	// in Delete API. And the second one is to maintain the lifecycle of
	// task in shim server.
	//
	// So, if the shim instance is running and task has been deleted in shim
	// server, the sb.Container.Task and task.Delete will receive the
	// ErrNotFound. If we don't delete the shim instance in io.containerd.service.v1.tasks-service,
	// shim will be leaky.
	//
	// Based on containerd/containerd#7496 issue, when host is under IO
	// pressure, the umount2 syscall will take more than 10 seconds so that
	// the CRI plugin will cancel this task.Delete call. However, the shim
	// server isn't aware about this. After return from umount2 syscall, the
	// shim server continue delete the task record. And then CRI plugin
	// retries to delete task and retrieves ErrNotFound and marks it as
	// stopped. Therefore, The shim is leaky.
	//
	// It's hard to handle the connection lost or request canceled cases in
	// shim server. We should call Delete API to io.containerd.service.v1.tasks-service
	// to ensure that shim instance is shutdown.
	//
	// REF:
	// 1. https://github.com/containerd/containerd/issues/7496#issuecomment-1671100968
	// 2. https://github.com/containerd/containerd/issues/8931
	if errdefs.IsNotFound(err) {
		_, err = c.client.TaskService().Delete(ctx, &apitasks.DeleteTaskRequest{ContainerID: cntr.Container.ID()})
		if err != nil {
			err = errdefs.FromGRPC(err)
			if !errdefs.IsNotFound(err) {
				return fmt.Errorf("failed to cleanup container %s in task-service: %w", cntr.Container.ID(), err)
			}
		}
		log.L.Infof("Ensure that container %s in task-service has been cleanup successfully", cntr.Container.ID())
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
			log.L.Debugf("Container %q transited from UNKNOWN to EXITED", cntr.ID)
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

// handleSandboxExit handles sandbox exit event.
func handleSandboxExit(ctx context.Context, sb sandboxstore.Sandbox, exitStatus uint32, exitTime time.Time, c *criService) error {
	if err := sb.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		status.State = sandboxstore.StateNotReady
		status.Pid = 0
		status.ExitStatus = exitStatus
		status.ExitedAt = exitTime
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
