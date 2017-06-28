// +build !windows

package shim

import (
	"path/filepath"

	events "github.com/containerd/containerd/api/services/events/v1"
	shimapi "github.com/containerd/containerd/linux/shim/v1"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// NewLocal returns a shim client implementation for issue commands to a shim
func NewLocal(s *Service) shimapi.ShimClient {
	return &local{
		s: s,
	}
}

type local struct {
	s *Service
}

func (c *local) Create(ctx context.Context, in *shimapi.CreateTaskRequest, opts ...grpc.CallOption) (*shimapi.CreateTaskResponse, error) {
	return c.s.Create(ctx, in)
}

func (c *local) Start(ctx context.Context, in *google_protobuf.Empty, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Start(ctx, in)
}

func (c *local) Delete(ctx context.Context, in *google_protobuf.Empty, opts ...grpc.CallOption) (*shimapi.DeleteResponse, error) {
	// make sure we unmount the containers rootfs for this local
	if err := unix.Unmount(filepath.Join(c.s.path, "rootfs"), 0); err != nil {
		return nil, err
	}
	return c.s.Delete(ctx, in)
}

func (c *local) DeleteProcess(ctx context.Context, in *shimapi.DeleteProcessRequest, opts ...grpc.CallOption) (*shimapi.DeleteResponse, error) {
	return c.s.DeleteProcess(ctx, in)
}

func (c *local) Exec(ctx context.Context, in *shimapi.ExecProcessRequest, opts ...grpc.CallOption) (*shimapi.ExecProcessResponse, error) {
	return c.s.Exec(ctx, in)
}

func (c *local) ResizePty(ctx context.Context, in *shimapi.ResizePtyRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.ResizePty(ctx, in)
}

func (c *local) Stream(ctx context.Context, in *shimapi.StreamEventsRequest, opts ...grpc.CallOption) (shimapi.Shim_StreamClient, error) {
	return &streamEvents{
		c:   c.s.events,
		ctx: ctx,
	}, nil
}

func (c *local) State(ctx context.Context, in *shimapi.StateRequest, opts ...grpc.CallOption) (*shimapi.StateResponse, error) {
	return c.s.State(ctx, in)
}

func (c *local) Pause(ctx context.Context, in *google_protobuf.Empty, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Pause(ctx, in)
}

func (c *local) Resume(ctx context.Context, in *google_protobuf.Empty, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Resume(ctx, in)
}

func (c *local) Kill(ctx context.Context, in *shimapi.KillRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Kill(ctx, in)
}

func (c *local) ListPids(ctx context.Context, in *shimapi.ListPidsRequest, opts ...grpc.CallOption) (*shimapi.ListPidsResponse, error) {
	return c.s.ListPids(ctx, in)
}

func (c *local) CloseIO(ctx context.Context, in *shimapi.CloseIORequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.CloseIO(ctx, in)
}

func (c *local) Checkpoint(ctx context.Context, in *shimapi.CheckpointTaskRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Checkpoint(ctx, in)
}

func (c *local) ShimInfo(ctx context.Context, in *google_protobuf.Empty, opts ...grpc.CallOption) (*shimapi.ShimInfoResponse, error) {
	return c.s.ShimInfo(ctx, in)
}

func (c *local) Update(ctx context.Context, in *shimapi.UpdateTaskRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Update(ctx, in)
}

type streamEvents struct {
	c   chan *events.RuntimeEvent
	ctx context.Context
}

func (e *streamEvents) Recv() (*events.RuntimeEvent, error) {
	ev := <-e.c
	return ev, nil
}

func (e *streamEvents) Header() (metadata.MD, error) {
	return nil, nil
}

func (e *streamEvents) Trailer() metadata.MD {
	return nil
}

func (e *streamEvents) CloseSend() error {
	return nil
}

func (e *streamEvents) Context() context.Context {
	return e.ctx
}

func (e *streamEvents) SendMsg(m interface{}) error {
	return nil
}

func (e *streamEvents) RecvMsg(m interface{}) error {
	return nil
}
