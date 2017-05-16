package shim

import (
	"path/filepath"

	shimapi "github.com/containerd/containerd/api/services/shim"
	"github.com/containerd/containerd/api/types/task"
	runc "github.com/containerd/go-runc"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func Client(path string) (shimapi.ShimClient, error) {
	pid, err := runc.ReadPidFile(filepath.Join(path, "init.pid"))
	if err != nil {
		return nil, err
	}

	cl := &client{
		s: New(path),
	}

	// used when quering  container status and info
	cl.s.initProcess = &initProcess{
		id:  filepath.Base(path),
		pid: pid,
		runc: &runc.Runc{
			Log:       filepath.Join(path, "log.json"),
			LogFormat: runc.JSON,
		},
	}
	return cl, nil
}

type client struct {
	s *Service
}

func (c *client) Create(ctx context.Context, in *shimapi.CreateRequest, opts ...grpc.CallOption) (*shimapi.CreateResponse, error) {
	return c.s.Create(ctx, in)
}

func (c *client) Start(ctx context.Context, in *shimapi.StartRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Start(ctx, in)
}

func (c *client) Delete(ctx context.Context, in *shimapi.DeleteRequest, opts ...grpc.CallOption) (*shimapi.DeleteResponse, error) {
	return c.s.Delete(ctx, in)
}

func (c *client) Exec(ctx context.Context, in *shimapi.ExecRequest, opts ...grpc.CallOption) (*shimapi.ExecResponse, error) {
	return c.s.Exec(ctx, in)
}

func (c *client) Pty(ctx context.Context, in *shimapi.PtyRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Pty(ctx, in)
}

func (c *client) Events(ctx context.Context, in *shimapi.EventsRequest, opts ...grpc.CallOption) (shimapi.Shim_EventsClient, error) {
	return &events{
		c:   c.s.events,
		ctx: ctx,
	}, nil
}

func (c *client) State(ctx context.Context, in *shimapi.StateRequest, opts ...grpc.CallOption) (*shimapi.StateResponse, error) {
	return c.s.State(ctx, in)
}

func (c *client) Pause(ctx context.Context, in *shimapi.PauseRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Pause(ctx, in)
}

func (c *client) Resume(ctx context.Context, in *shimapi.ResumeRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Resume(ctx, in)
}

func (c *client) Kill(ctx context.Context, in *shimapi.KillRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Kill(ctx, in)
}

func (c *client) Processes(ctx context.Context, in *shimapi.ProcessesRequest, opts ...grpc.CallOption) (*shimapi.ProcessesResponse, error) {
	return c.s.Processes(ctx, in)
}

func (c *client) Exit(ctx context.Context, in *shimapi.ExitRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	// don't exit the calling process for the client
	// but make sure we unmount the containers rootfs for this client
	if err := unix.Unmount(filepath.Join(c.s.path, "rootfs"), 0); err != nil {
		return nil, err
	}
	return empty, nil
}

func (c *client) CloseStdin(ctx context.Context, in *shimapi.CloseStdinRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.CloseStdin(ctx, in)
}

func (c *client) Checkpoint(ctx context.Context, in *shimapi.CheckpointRequest, opts ...grpc.CallOption) (*google_protobuf.Empty, error) {
	return c.s.Checkpoint(ctx, in)
}

type events struct {
	c   chan *task.Event
	ctx context.Context
}

func (e *events) Recv() (*task.Event, error) {
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
