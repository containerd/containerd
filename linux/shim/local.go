// +build !windows

package shim

import (
	"path/filepath"

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
	return &events{
		c:   c.s.events,
		ctx: ctx,
	}, nil
}

func (c *local) State(ctx context.Context, in *google_protobuf.Empty, opts ...grpc.CallOption) (*shimapi.StateResponse, error) {
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

func (c *local) ListProcesses(ctx context.Context, in *shimapi.ListProcessesRequest, opts ...grpc.CallOption) (*shimapi.ListProcessesResponse, error) {
	return c.s.ListProcesses(ctx, in)
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

type events struct {
	c   chan *shimapi.Event
	ctx context.Context
}

func (e *events) Recv() (*shimapi.Event, error) {
	ev := <-e.c
	return ev, nil
}

func (e *events) Header() (metadata.MD, error) {
	return nil, nil
}

func (e *events) Trailer() metadata.MD {
	return nil
}

func (e *events) CloseSend() error {
	return nil
}

func (e *events) Context() context.Context {
	return e.ctx
}

func (e *events) SendMsg(m interface{}) error {
	return nil
}

func (e *events) RecvMsg(m interface{}) error {
	return nil
}
