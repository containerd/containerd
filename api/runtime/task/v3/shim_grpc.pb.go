// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.20.1
// source: github.com/containerd/containerd/api/runtime/task/v3/shim.proto

package task

import (
	context "context"
	types "github.com/containerd/containerd/v2/api/types"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// TaskClient is the client API for Task service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type TaskClient interface {
	State(ctx context.Context, in *StateRequest, opts ...grpc.CallOption) (*StateResponse, error)
	Create(ctx context.Context, in *CreateTaskRequest, opts ...grpc.CallOption) (*CreateTaskResponse, error)
	Start(ctx context.Context, in *StartRequest, opts ...grpc.CallOption) (*StartResponse, error)
	Delete(ctx context.Context, in *DeleteRequest, opts ...grpc.CallOption) (*DeleteResponse, error)
	Pids(ctx context.Context, in *PidsRequest, opts ...grpc.CallOption) (*PidsResponse, error)
	Pause(ctx context.Context, in *PauseRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Resume(ctx context.Context, in *ResumeRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Checkpoint(ctx context.Context, in *CheckpointTaskRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Kill(ctx context.Context, in *KillRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Exec(ctx context.Context, in *ExecProcessRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	ResizePty(ctx context.Context, in *ResizePtyRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	CloseIO(ctx context.Context, in *CloseIORequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Update(ctx context.Context, in *UpdateTaskRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Wait(ctx context.Context, in *WaitRequest, opts ...grpc.CallOption) (*WaitResponse, error)
	Stats(ctx context.Context, in *StatsRequest, opts ...grpc.CallOption) (*StatsResponse, error)
	Connect(ctx context.Context, in *ConnectRequest, opts ...grpc.CallOption) (*ConnectResponse, error)
	Shutdown(ctx context.Context, in *ShutdownRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	// Events allows containerd subscribe to a stream of shim events.
	// Up to 2.0, containerd offered a separate daemon's endpoint to submit events from shim.
	// This required a separate connection from shim back to containerd daemon.
	// Since both TTRPC and GRPC backends now support streaming, this endpoint
	// allows to optimzie resources and utilize just one bidirectional connection.
	//
	// This feature is optional and not enabled by default. A shim that supports this
	// must return `shim.EventStreaming` feature in bootstrap params.
	Events(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (Task_EventsClient, error)
}

type taskClient struct {
	cc grpc.ClientConnInterface
}

func NewTaskClient(cc grpc.ClientConnInterface) TaskClient {
	return &taskClient{cc}
}

func (c *taskClient) State(ctx context.Context, in *StateRequest, opts ...grpc.CallOption) (*StateResponse, error) {
	out := new(StateResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/State", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Create(ctx context.Context, in *CreateTaskRequest, opts ...grpc.CallOption) (*CreateTaskResponse, error) {
	out := new(CreateTaskResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Create", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Start(ctx context.Context, in *StartRequest, opts ...grpc.CallOption) (*StartResponse, error) {
	out := new(StartResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Start", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Delete(ctx context.Context, in *DeleteRequest, opts ...grpc.CallOption) (*DeleteResponse, error) {
	out := new(DeleteResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Delete", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Pids(ctx context.Context, in *PidsRequest, opts ...grpc.CallOption) (*PidsResponse, error) {
	out := new(PidsResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Pids", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Pause(ctx context.Context, in *PauseRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Pause", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Resume(ctx context.Context, in *ResumeRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Resume", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Checkpoint(ctx context.Context, in *CheckpointTaskRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Checkpoint", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Kill(ctx context.Context, in *KillRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Kill", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Exec(ctx context.Context, in *ExecProcessRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Exec", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) ResizePty(ctx context.Context, in *ResizePtyRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/ResizePty", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) CloseIO(ctx context.Context, in *CloseIORequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/CloseIO", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Update(ctx context.Context, in *UpdateTaskRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Update", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Wait(ctx context.Context, in *WaitRequest, opts ...grpc.CallOption) (*WaitResponse, error) {
	out := new(WaitResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Wait", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Stats(ctx context.Context, in *StatsRequest, opts ...grpc.CallOption) (*StatsResponse, error) {
	out := new(StatsResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Stats", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Connect(ctx context.Context, in *ConnectRequest, opts ...grpc.CallOption) (*ConnectResponse, error) {
	out := new(ConnectResponse)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Connect", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Shutdown(ctx context.Context, in *ShutdownRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.task.v3.Task/Shutdown", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *taskClient) Events(ctx context.Context, in *emptypb.Empty, opts ...grpc.CallOption) (Task_EventsClient, error) {
	stream, err := c.cc.NewStream(ctx, &Task_ServiceDesc.Streams[0], "/containerd.task.v3.Task/Events", opts...)
	if err != nil {
		return nil, err
	}
	x := &taskEventsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Task_EventsClient interface {
	Recv() (*types.Envelope, error)
	grpc.ClientStream
}

type taskEventsClient struct {
	grpc.ClientStream
}

func (x *taskEventsClient) Recv() (*types.Envelope, error) {
	m := new(types.Envelope)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// TaskServer is the server API for Task service.
// All implementations must embed UnimplementedTaskServer
// for forward compatibility
type TaskServer interface {
	State(context.Context, *StateRequest) (*StateResponse, error)
	Create(context.Context, *CreateTaskRequest) (*CreateTaskResponse, error)
	Start(context.Context, *StartRequest) (*StartResponse, error)
	Delete(context.Context, *DeleteRequest) (*DeleteResponse, error)
	Pids(context.Context, *PidsRequest) (*PidsResponse, error)
	Pause(context.Context, *PauseRequest) (*emptypb.Empty, error)
	Resume(context.Context, *ResumeRequest) (*emptypb.Empty, error)
	Checkpoint(context.Context, *CheckpointTaskRequest) (*emptypb.Empty, error)
	Kill(context.Context, *KillRequest) (*emptypb.Empty, error)
	Exec(context.Context, *ExecProcessRequest) (*emptypb.Empty, error)
	ResizePty(context.Context, *ResizePtyRequest) (*emptypb.Empty, error)
	CloseIO(context.Context, *CloseIORequest) (*emptypb.Empty, error)
	Update(context.Context, *UpdateTaskRequest) (*emptypb.Empty, error)
	Wait(context.Context, *WaitRequest) (*WaitResponse, error)
	Stats(context.Context, *StatsRequest) (*StatsResponse, error)
	Connect(context.Context, *ConnectRequest) (*ConnectResponse, error)
	Shutdown(context.Context, *ShutdownRequest) (*emptypb.Empty, error)
	// Events allows containerd subscribe to a stream of shim events.
	// Up to 2.0, containerd offered a separate daemon's endpoint to submit events from shim.
	// This required a separate connection from shim back to containerd daemon.
	// Since both TTRPC and GRPC backends now support streaming, this endpoint
	// allows to optimzie resources and utilize just one bidirectional connection.
	//
	// This feature is optional and not enabled by default. A shim that supports this
	// must return `shim.EventStreaming` feature in bootstrap params.
	Events(*emptypb.Empty, Task_EventsServer) error
	mustEmbedUnimplementedTaskServer()
}

// UnimplementedTaskServer must be embedded to have forward compatible implementations.
type UnimplementedTaskServer struct {
}

func (UnimplementedTaskServer) State(context.Context, *StateRequest) (*StateResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method State not implemented")
}
func (UnimplementedTaskServer) Create(context.Context, *CreateTaskRequest) (*CreateTaskResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Create not implemented")
}
func (UnimplementedTaskServer) Start(context.Context, *StartRequest) (*StartResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Start not implemented")
}
func (UnimplementedTaskServer) Delete(context.Context, *DeleteRequest) (*DeleteResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Delete not implemented")
}
func (UnimplementedTaskServer) Pids(context.Context, *PidsRequest) (*PidsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Pids not implemented")
}
func (UnimplementedTaskServer) Pause(context.Context, *PauseRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Pause not implemented")
}
func (UnimplementedTaskServer) Resume(context.Context, *ResumeRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Resume not implemented")
}
func (UnimplementedTaskServer) Checkpoint(context.Context, *CheckpointTaskRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Checkpoint not implemented")
}
func (UnimplementedTaskServer) Kill(context.Context, *KillRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Kill not implemented")
}
func (UnimplementedTaskServer) Exec(context.Context, *ExecProcessRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Exec not implemented")
}
func (UnimplementedTaskServer) ResizePty(context.Context, *ResizePtyRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ResizePty not implemented")
}
func (UnimplementedTaskServer) CloseIO(context.Context, *CloseIORequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CloseIO not implemented")
}
func (UnimplementedTaskServer) Update(context.Context, *UpdateTaskRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Update not implemented")
}
func (UnimplementedTaskServer) Wait(context.Context, *WaitRequest) (*WaitResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Wait not implemented")
}
func (UnimplementedTaskServer) Stats(context.Context, *StatsRequest) (*StatsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Stats not implemented")
}
func (UnimplementedTaskServer) Connect(context.Context, *ConnectRequest) (*ConnectResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Connect not implemented")
}
func (UnimplementedTaskServer) Shutdown(context.Context, *ShutdownRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Shutdown not implemented")
}
func (UnimplementedTaskServer) Events(*emptypb.Empty, Task_EventsServer) error {
	return status.Errorf(codes.Unimplemented, "method Events not implemented")
}
func (UnimplementedTaskServer) mustEmbedUnimplementedTaskServer() {}

// UnsafeTaskServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to TaskServer will
// result in compilation errors.
type UnsafeTaskServer interface {
	mustEmbedUnimplementedTaskServer()
}

func RegisterTaskServer(s grpc.ServiceRegistrar, srv TaskServer) {
	s.RegisterService(&Task_ServiceDesc, srv)
}

func _Task_State_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).State(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/State",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).State(ctx, req.(*StateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Create_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateTaskRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Create(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Create",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Create(ctx, req.(*CreateTaskRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Start_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Start(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Start",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Start(ctx, req.(*StartRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Delete_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Delete(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Delete",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Delete(ctx, req.(*DeleteRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Pids_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PidsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Pids(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Pids",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Pids(ctx, req.(*PidsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Pause_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PauseRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Pause(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Pause",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Pause(ctx, req.(*PauseRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Resume_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ResumeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Resume(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Resume",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Resume(ctx, req.(*ResumeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Checkpoint_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CheckpointTaskRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Checkpoint(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Checkpoint",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Checkpoint(ctx, req.(*CheckpointTaskRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Kill_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(KillRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Kill(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Kill",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Kill(ctx, req.(*KillRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Exec_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ExecProcessRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Exec(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Exec",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Exec(ctx, req.(*ExecProcessRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_ResizePty_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ResizePtyRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).ResizePty(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/ResizePty",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).ResizePty(ctx, req.(*ResizePtyRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_CloseIO_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CloseIORequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).CloseIO(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/CloseIO",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).CloseIO(ctx, req.(*CloseIORequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Update_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateTaskRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Update(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Update",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Update(ctx, req.(*UpdateTaskRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Wait_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(WaitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Wait(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Wait",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Wait(ctx, req.(*WaitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Stats_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StatsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Stats(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Stats",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Stats(ctx, req.(*StatsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Connect_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ConnectRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Connect(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Connect",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Connect(ctx, req.(*ConnectRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Shutdown_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ShutdownRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(TaskServer).Shutdown(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.task.v3.Task/Shutdown",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(TaskServer).Shutdown(ctx, req.(*ShutdownRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Task_Events_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(emptypb.Empty)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(TaskServer).Events(m, &taskEventsServer{stream})
}

type Task_EventsServer interface {
	Send(*types.Envelope) error
	grpc.ServerStream
}

type taskEventsServer struct {
	grpc.ServerStream
}

func (x *taskEventsServer) Send(m *types.Envelope) error {
	return x.ServerStream.SendMsg(m)
}

// Task_ServiceDesc is the grpc.ServiceDesc for Task service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Task_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "containerd.task.v3.Task",
	HandlerType: (*TaskServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "State",
			Handler:    _Task_State_Handler,
		},
		{
			MethodName: "Create",
			Handler:    _Task_Create_Handler,
		},
		{
			MethodName: "Start",
			Handler:    _Task_Start_Handler,
		},
		{
			MethodName: "Delete",
			Handler:    _Task_Delete_Handler,
		},
		{
			MethodName: "Pids",
			Handler:    _Task_Pids_Handler,
		},
		{
			MethodName: "Pause",
			Handler:    _Task_Pause_Handler,
		},
		{
			MethodName: "Resume",
			Handler:    _Task_Resume_Handler,
		},
		{
			MethodName: "Checkpoint",
			Handler:    _Task_Checkpoint_Handler,
		},
		{
			MethodName: "Kill",
			Handler:    _Task_Kill_Handler,
		},
		{
			MethodName: "Exec",
			Handler:    _Task_Exec_Handler,
		},
		{
			MethodName: "ResizePty",
			Handler:    _Task_ResizePty_Handler,
		},
		{
			MethodName: "CloseIO",
			Handler:    _Task_CloseIO_Handler,
		},
		{
			MethodName: "Update",
			Handler:    _Task_Update_Handler,
		},
		{
			MethodName: "Wait",
			Handler:    _Task_Wait_Handler,
		},
		{
			MethodName: "Stats",
			Handler:    _Task_Stats_Handler,
		},
		{
			MethodName: "Connect",
			Handler:    _Task_Connect_Handler,
		},
		{
			MethodName: "Shutdown",
			Handler:    _Task_Shutdown_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Events",
			Handler:       _Task_Events_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "github.com/containerd/containerd/api/runtime/task/v3/shim.proto",
}
