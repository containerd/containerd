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

package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/pkg/atomicfile"
	"github.com/containerd/containerd/v2/pkg/dialer"
	"github.com/containerd/ttrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	eventstypes "github.com/containerd/containerd/v2/api/events"
	task "github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/errdefs"
	"github.com/containerd/containerd/v2/events/exchange"
	"github.com/containerd/containerd/v2/pkg/timeout"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/containerd/containerd/v2/runtime"
	client "github.com/containerd/containerd/v2/runtime/v2/shim"
	"github.com/containerd/log"
)

const (
	loadTimeout     = "io.containerd.timeout.shim.load"
	cleanupTimeout  = "io.containerd.timeout.shim.cleanup"
	shutdownTimeout = "io.containerd.timeout.shim.shutdown"
)

func init() {
	timeout.Set(loadTimeout, 5*time.Second)
	timeout.Set(cleanupTimeout, 5*time.Second)
	timeout.Set(shutdownTimeout, 3*time.Second)
}

func loadShim(ctx context.Context, bundle *Bundle, onClose func()) (_ ShimInstance, retErr error) {
	shimCtx, cancelShimLog := context.WithCancel(ctx)
	defer func() {
		if retErr != nil {
			cancelShimLog()
		}
	}()
	f, err := openShimLog(shimCtx, bundle, client.AnonReconnectDialer)
	if err != nil {
		return nil, fmt.Errorf("open shim log pipe when reload: %w", err)
	}
	defer func() {
		if retErr != nil {
			f.Close()
		}
	}()
	// open the log pipe and block until the writer is ready
	// this helps with synchronization of the shim
	// copy the shim's logs to containerd's output
	go func() {
		defer f.Close()
		_, err := io.Copy(os.Stderr, f)
		// To prevent flood of error messages, the expected error
		// should be reset, like os.ErrClosed or os.ErrNotExist, which
		// depends on platform.
		err = checkCopyShimLogError(ctx, err)
		if err != nil {
			log.G(ctx).WithError(err).Error("copy shim log after reload")
		}
	}()
	onCloseWithShimLog := func() {
		onClose()
		cancelShimLog()
		f.Close()
	}

	params, err := restoreBootstrapParams(bundle.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read boostrap.json when restoring bundle %q: %w", bundle.ID, err)
	}

	conn, err := makeConnection(ctx, bundle.ID, params, onCloseWithShimLog)
	if err != nil {
		return nil, fmt.Errorf("unable to make connection: %w", err)
	}

	defer func() {
		if retErr != nil {
			conn.Close()
		}
	}()

	shim := &shim{
		bundle:  bundle,
		client:  conn,
		version: params.Version,
		address: params.Address,
	}

	return shim, nil
}

func cleanupAfterDeadShim(ctx context.Context, id string, rt *runtime.NSMap[ShimInstance], events *exchange.Exchange, binaryCall *binary) {
	ctx, cancel := timeout.WithContext(ctx, cleanupTimeout)
	defer cancel()

	log.G(ctx).WithField("id", id).Warn("cleaning up after shim disconnected")
	response, err := binaryCall.Delete(ctx)
	if err != nil {
		log.G(ctx).WithError(err).WithField("id", id).Warn("failed to clean up after shim disconnected")
	}

	if _, err := rt.Get(ctx, id); err != nil {
		// Task was never started or was already successfully deleted
		// No need to publish events
		return
	}

	var (
		pid        uint32
		exitStatus uint32
		exitedAt   time.Time
	)
	if response != nil {
		pid = response.Pid
		exitStatus = response.Status
		exitedAt = response.Timestamp
	} else {
		exitStatus = 255
		exitedAt = time.Now()
	}
	events.Publish(ctx, runtime.TaskExitEventTopic, &eventstypes.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         pid,
		ExitStatus:  exitStatus,
		ExitedAt:    protobuf.ToTimestamp(exitedAt),
	})

	events.Publish(ctx, runtime.TaskDeleteEventTopic, &eventstypes.TaskDelete{
		ContainerID: id,
		Pid:         pid,
		ExitStatus:  exitStatus,
		ExitedAt:    protobuf.ToTimestamp(exitedAt),
	})
}

// CurrentShimVersion is the latest shim version supported by containerd (e.g. TaskService v3).
const CurrentShimVersion = 3

// ShimInstance represents running shim process managed by ShimManager.
type ShimInstance interface {
	io.Closer

	// ID of the shim.
	ID() string
	// Namespace of this shim.
	Namespace() string
	// Bundle is a file system path to shim's bundle.
	Bundle() string
	// Client returns the underlying TTRPC or GRPC client object for this shim.
	// The underlying object can be either *ttrpc.Client or grpc.ClientConnInterface.
	Client() any
	// Delete will close the client and remove bundle from disk.
	Delete(ctx context.Context) error
	// Version returns shim's features compatibility version.
	Version() int
	// Address returns shim's address to connect for task and sandbox api
	Address() string
}

func parseStartResponse(response []byte) (client.BootstrapParams, error) {
	var params client.BootstrapParams

	if err := json.Unmarshal(response, &params); err != nil || params.Version < 2 {
		// Use TTRPC for legacy shims
		params.Address = string(response)
		params.Protocol = "ttrpc"
		params.Version = 2
	}

	if params.Version > CurrentShimVersion {
		return client.BootstrapParams{}, fmt.Errorf("unsupported shim version (%d): %w", params.Version, errdefs.ErrNotImplemented)
	}

	return params, nil
}

// writeBootstrapParams writes shim's bootstrap configuration (e.g. how to connect, version, etc).
func writeBootstrapParams(path string, params client.BootstrapParams) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	data, err := json.Marshal(&params)
	if err != nil {
		return err
	}

	f, err := atomicfile.New(path, 0o666)
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err != nil {
		f.Cancel()
		return err
	}

	return f.Close()
}

func readBootstrapParams(path string) (client.BootstrapParams, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return client.BootstrapParams{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return client.BootstrapParams{}, err
	}

	return parseStartResponse(data)
}

// makeConnection creates a new TTRPC or GRPC connection object from address.
// address can be either a socket path for TTRPC or JSON serialized BootstrapParams.
func makeConnection(ctx context.Context, id string, params client.BootstrapParams, onClose func()) (_ io.Closer, retErr error) {
	log.G(ctx).WithFields(log.Fields{
		"address":  params.Address,
		"protocol": params.Protocol,
		"version":  params.Version,
	}).Infof("connecting to shim %s", id)

	switch strings.ToLower(params.Protocol) {
	case "ttrpc":
		conn, err := client.Connect(params.Address, client.AnonReconnectDialer)
		if err != nil {
			return nil, fmt.Errorf("failed to create TTRPC connection: %w", err)
		}
		defer func() {
			if retErr != nil {
				conn.Close()
			}
		}()

		return ttrpc.NewClient(conn, ttrpc.WithOnClose(onClose)), nil
	case "grpc":
		ctx, cancel := context.WithTimeout(ctx, time.Second*100)
		defer cancel()

		gopts := []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		}
		return grpcDialContext(ctx, params.Address, onClose, gopts...)
	default:
		return nil, fmt.Errorf("unexpected protocol: %q", params.Protocol)
	}
}

// grpcDialContext and the underlying grpcConn type exist solely
// so we can have something similar to ttrpc.WithOnClose to have
// a callback run when the connection is severed or explicitly closed.
func grpcDialContext(
	ctx context.Context,
	address string,
	onClose func(),
	gopts ...grpc.DialOption,
) (*grpcConn, error) {
	// If grpc.WithBlock is specified in gopts this causes the connection to block waiting for
	// a connection regardless of if the socket exists or has a listener when Dial begins. This
	// specific behavior of WithBlock is mostly undesirable for shims, as if the socket isn't
	// there when we go to load/connect there's likely an issue. However, getting rid of WithBlock is
	// also undesirable as we don't want the background connection behavior, we want to ensure
	// a connection before moving on. To bring this in line with the ttrpc connection behavior
	// lets do an initial dial to ensure the shims socket is actually available. stat wouldn't suffice
	// here as if the shim exited unexpectedly its socket may still be on the filesystem, but it'd return
	// ECONNREFUSED which grpc.DialContext will happily trudge along through for the full timeout.
	//
	// This is especially helpful on restart of containerd as if the shim died while containerd
	// was down, we end up waiting the full timeout.
	conn, err := net.DialTimeout("unix", address, time.Second*10)
	if err != nil {
		return nil, err
	}
	conn.Close()

	target := dialer.DialAddress(address)
	client, err := grpc.DialContext(ctx, target, gopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GRPC connection: %w", err)
	}

	done := make(chan struct{})
	go func() {
		gctx := context.Background()
		sourceState := connectivity.Ready
		for {
			if client.WaitForStateChange(gctx, sourceState) {
				state := client.GetState()
				if state == connectivity.Idle || state == connectivity.Shutdown {
					break
				}
				// Could be transient failure. Lets see if we can get back to a working
				// state.
				log.G(gctx).WithFields(log.Fields{
					"state": state,
					"addr":  target,
				}).Warn("shim grpc connection unexpected state")
				sourceState = state
			}
		}
		onClose()
		close(done)
	}()

	return &grpcConn{
		ClientConn:  client,
		onCloseDone: done,
	}, nil
}

type grpcConn struct {
	*grpc.ClientConn
	onCloseDone chan struct{}
}

func (gc *grpcConn) UserOnCloseWait(ctx context.Context) error {
	select {
	case <-gc.onCloseDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type shim struct {
	bundle  *Bundle
	client  any
	version int
	address string
}

var _ ShimInstance = (*shim)(nil)

// ID of the shim/task
func (s *shim) ID() string {
	return s.bundle.ID
}

func (s *shim) Version() int {
	return s.version
}

func (s *shim) Namespace() string {
	return s.bundle.Namespace
}

func (s *shim) Bundle() string {
	return s.bundle.Path
}

func (s *shim) Client() any {
	return s.client
}

func (s *shim) Address() string {
	return s.address
}

// Close closes the underlying client connection.
func (s *shim) Close() error {
	if ttrpcClient, ok := s.client.(*ttrpc.Client); ok {
		return ttrpcClient.Close()
	}

	if grpcClient, ok := s.client.(*grpcConn); ok {
		return grpcClient.Close()
	}

	return nil
}

func (s *shim) Delete(ctx context.Context) error {
	var result []error

	if ttrpcClient, ok := s.client.(*ttrpc.Client); ok {
		if err := ttrpcClient.Close(); err != nil {
			result = append(result, fmt.Errorf("failed to close ttrpc client: %w", err))
		}

		if err := ttrpcClient.UserOnCloseWait(ctx); err != nil {
			result = append(result, fmt.Errorf("close wait error: %w", err))
		}
	}

	if grpcClient, ok := s.client.(*grpcConn); ok {
		if err := grpcClient.Close(); err != nil {
			result = append(result, fmt.Errorf("failed to close grpc client: %w", err))
		}

		if err := grpcClient.UserOnCloseWait(ctx); err != nil {
			result = append(result, fmt.Errorf("close wait error: %w", err))
		}
	}

	if err := s.bundle.Delete(); err != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to delete bundle")
		result = append(result, fmt.Errorf("failed to delete bundle: %w", err))
	}

	return errors.Join(result...)
}

var _ runtime.Task = &shimTask{}

// shimTask wraps shim process and adds task service client for compatibility with existing shim manager.
type shimTask struct {
	ShimInstance
	*remoteTask
}

func newShimTask(shim ShimInstance) (*shimTask, error) {
	taskClient, err := NewTaskClient(shim.Client(), shim.Version())
	if err != nil {
		return nil, err
	}

	return &shimTask{
		ShimInstance: shim,
		remoteTask: &remoteTask{
			id:         shim.ID(),
			taskClient: taskClient,
		},
	}, nil
}

func (s *shimTask) Shutdown(ctx context.Context) error {
	_, err := s.remoteTask.taskClient.Shutdown(ctx, &task.ShutdownRequest{
		ID: s.ID(),
	})
	if err != nil && !errors.Is(err, ttrpc.ErrClosed) {
		return errdefs.FromGRPC(err)
	}
	return nil
}

func (s *shimTask) waitShutdown(ctx context.Context) error {
	ctx, cancel := timeout.WithContext(ctx, shutdownTimeout)
	defer cancel()
	return s.Shutdown(ctx)
}

func (s *shimTask) delete(ctx context.Context, removeTask func(ctx context.Context, id string)) (*runtime.Exit, error) {
	response, shimErr := s.remoteTask.taskClient.Delete(ctx, &task.DeleteRequest{
		ID: s.ID(),
	})
	if shimErr != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(shimErr).Debug("failed to delete task")
		if !errors.Is(shimErr, ttrpc.ErrClosed) {
			shimErr = errdefs.FromGRPC(shimErr)
			if !errdefs.IsNotFound(shimErr) {
				return nil, shimErr
			}
		}
	}

	// NOTE: If the shim has been killed and ttrpc connection has been
	// closed, the shimErr will not be nil. For this case, the event
	// subscriber, like moby/moby, might have received the exit or delete
	// events. Just in case, we should allow ttrpc-callback-on-close to
	// send the exit and delete events again. And the exit status will
	// depend on result of shimV2.Delete.
	//
	// If not, the shim has been delivered the exit and delete events.
	// So we should remove the record and prevent duplicate events from
	// ttrpc-callback-on-close.
	//
	// TODO: It's hard to guarantee that the event is unique and sent only
	// once. The moby/moby should not rely on that assumption that there is
	// only one exit event. The moby/moby should handle the duplicate events.
	//
	// REF: https://github.com/containerd/containerd/issues/4769
	if shimErr == nil {
		removeTask(ctx, s.ID())
	}

	if err := s.waitShutdown(ctx); err != nil {
		// FIXME(fuweid):
		//
		// If the error is context canceled, should we use context.TODO()
		// to wait for it?
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to shutdown shim task and the shim might be leaked")
	}

	if err := s.ShimInstance.Delete(ctx); err != nil {
		log.G(ctx).WithField("id", s.ID()).WithError(err).Error("failed to delete shim")
	}

	// remove self from the runtime task list
	// this seems dirty but it cleans up the API across runtimes, tasks, and the service
	removeTask(ctx, s.ID())

	if shimErr != nil {
		return nil, shimErr
	}

	return &runtime.Exit{
		Status:    response.ExitStatus,
		Timestamp: protobuf.FromTimestamp(response.ExitedAt),
		Pid:       response.Pid,
	}, nil
}

func (s *shimTask) Create(ctx context.Context, opts runtime.CreateOpts) (runtime.Task, error) {
	if err := s.remoteTask.Create(ctx, s.Bundle(), opts); err != nil {
		return nil, err
	}
	return s, nil
}
