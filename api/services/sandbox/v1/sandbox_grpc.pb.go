// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.20.1
// source: github.com/containerd/containerd/api/services/sandbox/v1/sandbox.proto

package sandbox

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// ControllerClient is the client API for Controller service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type ControllerClient interface {
	Start(ctx context.Context, in *ControllerStartRequest, opts ...grpc.CallOption) (*ControllerStartResponse, error)
	Shutdown(ctx context.Context, in *ControllerShutdownRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Pause(ctx context.Context, in *ControllerPauseRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Resume(ctx context.Context, in *ControllerResumeRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Update(ctx context.Context, in *ControllerUpdateRequest, opts ...grpc.CallOption) (*ControllerUpdateResponse, error)
	AppendContainer(ctx context.Context, in *ControllerAppendContainerRequest, opts ...grpc.CallOption) (*ControllerAppendContainerResponse, error)
	UpdateContainer(ctx context.Context, in *ControllerUpdateContainerRequest, opts ...grpc.CallOption) (*ControllerUpdateContainerResponse, error)
	RemoveContainer(ctx context.Context, in *ControllerRemoveContainerRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	Status(ctx context.Context, in *ControllerStatusRequest, opts ...grpc.CallOption) (*ControllerStatusResponse, error)
	Ping(ctx context.Context, in *ControllerPingRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
}

type controllerClient struct {
	cc grpc.ClientConnInterface
}

func NewControllerClient(cc grpc.ClientConnInterface) ControllerClient {
	return &controllerClient{cc}
}

func (c *controllerClient) Start(ctx context.Context, in *ControllerStartRequest, opts ...grpc.CallOption) (*ControllerStartResponse, error) {
	out := new(ControllerStartResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Start", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) Shutdown(ctx context.Context, in *ControllerShutdownRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Shutdown", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) Pause(ctx context.Context, in *ControllerPauseRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Pause", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) Resume(ctx context.Context, in *ControllerResumeRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Resume", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) Update(ctx context.Context, in *ControllerUpdateRequest, opts ...grpc.CallOption) (*ControllerUpdateResponse, error) {
	out := new(ControllerUpdateResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Update", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) AppendContainer(ctx context.Context, in *ControllerAppendContainerRequest, opts ...grpc.CallOption) (*ControllerAppendContainerResponse, error) {
	out := new(ControllerAppendContainerResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/AppendContainer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) UpdateContainer(ctx context.Context, in *ControllerUpdateContainerRequest, opts ...grpc.CallOption) (*ControllerUpdateContainerResponse, error) {
	out := new(ControllerUpdateContainerResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/UpdateContainer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) RemoveContainer(ctx context.Context, in *ControllerRemoveContainerRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/RemoveContainer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) Status(ctx context.Context, in *ControllerStatusRequest, opts ...grpc.CallOption) (*ControllerStatusResponse, error) {
	out := new(ControllerStatusResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Status", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controllerClient) Ping(ctx context.Context, in *ControllerPingRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Controller/Ping", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ControllerServer is the server API for Controller service.
// All implementations must embed UnimplementedControllerServer
// for forward compatibility
type ControllerServer interface {
	Start(context.Context, *ControllerStartRequest) (*ControllerStartResponse, error)
	Shutdown(context.Context, *ControllerShutdownRequest) (*emptypb.Empty, error)
	Pause(context.Context, *ControllerPauseRequest) (*emptypb.Empty, error)
	Resume(context.Context, *ControllerResumeRequest) (*emptypb.Empty, error)
	Update(context.Context, *ControllerUpdateRequest) (*ControllerUpdateResponse, error)
	AppendContainer(context.Context, *ControllerAppendContainerRequest) (*ControllerAppendContainerResponse, error)
	UpdateContainer(context.Context, *ControllerUpdateContainerRequest) (*ControllerUpdateContainerResponse, error)
	RemoveContainer(context.Context, *ControllerRemoveContainerRequest) (*emptypb.Empty, error)
	Status(context.Context, *ControllerStatusRequest) (*ControllerStatusResponse, error)
	Ping(context.Context, *ControllerPingRequest) (*emptypb.Empty, error)
	mustEmbedUnimplementedControllerServer()
}

// UnimplementedControllerServer must be embedded to have forward compatible implementations.
type UnimplementedControllerServer struct {
}

func (UnimplementedControllerServer) Start(context.Context, *ControllerStartRequest) (*ControllerStartResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Start not implemented")
}
func (UnimplementedControllerServer) Shutdown(context.Context, *ControllerShutdownRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Shutdown not implemented")
}
func (UnimplementedControllerServer) Pause(context.Context, *ControllerPauseRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Pause not implemented")
}
func (UnimplementedControllerServer) Resume(context.Context, *ControllerResumeRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Resume not implemented")
}
func (UnimplementedControllerServer) Update(context.Context, *ControllerUpdateRequest) (*ControllerUpdateResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Update not implemented")
}
func (UnimplementedControllerServer) AppendContainer(context.Context, *ControllerAppendContainerRequest) (*ControllerAppendContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AppendContainer not implemented")
}
func (UnimplementedControllerServer) UpdateContainer(context.Context, *ControllerUpdateContainerRequest) (*ControllerUpdateContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdateContainer not implemented")
}
func (UnimplementedControllerServer) RemoveContainer(context.Context, *ControllerRemoveContainerRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RemoveContainer not implemented")
}
func (UnimplementedControllerServer) Status(context.Context, *ControllerStatusRequest) (*ControllerStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Status not implemented")
}
func (UnimplementedControllerServer) Ping(context.Context, *ControllerPingRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Ping not implemented")
}
func (UnimplementedControllerServer) mustEmbedUnimplementedControllerServer() {}

// UnsafeControllerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to ControllerServer will
// result in compilation errors.
type UnsafeControllerServer interface {
	mustEmbedUnimplementedControllerServer()
}

func RegisterControllerServer(s grpc.ServiceRegistrar, srv ControllerServer) {
	s.RegisterService(&Controller_ServiceDesc, srv)
}

func _Controller_Start_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerStartRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Start(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Start",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Start(ctx, req.(*ControllerStartRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_Shutdown_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerShutdownRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Shutdown(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Shutdown",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Shutdown(ctx, req.(*ControllerShutdownRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_Pause_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerPauseRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Pause(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Pause",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Pause(ctx, req.(*ControllerPauseRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_Resume_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerResumeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Resume(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Resume",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Resume(ctx, req.(*ControllerResumeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_Update_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerUpdateRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Update(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Update",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Update(ctx, req.(*ControllerUpdateRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_AppendContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerAppendContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).AppendContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/AppendContainer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).AppendContainer(ctx, req.(*ControllerAppendContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_UpdateContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerUpdateContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).UpdateContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/UpdateContainer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).UpdateContainer(ctx, req.(*ControllerUpdateContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_RemoveContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerRemoveContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).RemoveContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/RemoveContainer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).RemoveContainer(ctx, req.(*ControllerRemoveContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_Status_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerStatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Status(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Status",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Status(ctx, req.(*ControllerStatusRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Controller_Ping_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ControllerPingRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControllerServer).Ping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Controller/Ping",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(ControllerServer).Ping(ctx, req.(*ControllerPingRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Controller_ServiceDesc is the grpc.ServiceDesc for Controller service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Controller_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "containerd.services.sandbox.v1.Controller",
	HandlerType: (*ControllerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Start",
			Handler:    _Controller_Start_Handler,
		},
		{
			MethodName: "Shutdown",
			Handler:    _Controller_Shutdown_Handler,
		},
		{
			MethodName: "Pause",
			Handler:    _Controller_Pause_Handler,
		},
		{
			MethodName: "Resume",
			Handler:    _Controller_Resume_Handler,
		},
		{
			MethodName: "Update",
			Handler:    _Controller_Update_Handler,
		},
		{
			MethodName: "AppendContainer",
			Handler:    _Controller_AppendContainer_Handler,
		},
		{
			MethodName: "UpdateContainer",
			Handler:    _Controller_UpdateContainer_Handler,
		},
		{
			MethodName: "RemoveContainer",
			Handler:    _Controller_RemoveContainer_Handler,
		},
		{
			MethodName: "Status",
			Handler:    _Controller_Status_Handler,
		},
		{
			MethodName: "Ping",
			Handler:    _Controller_Ping_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "github.com/containerd/containerd/api/services/sandbox/v1/sandbox.proto",
}

// SandboxerClient is the client API for Sandboxer service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type SandboxerClient interface {
	// Create create a new sandbox, as there is only "Create" and "Delete" in sandbox lifecycle,
	// after creation, the sandbox should be running.
	Create(ctx context.Context, in *CreateSandboxRequest, opts ...grpc.CallOption) (*CreateSandboxResponse, error)
	// Update update a sandbox metadata, it only updates spec/labels/extensions of this sandbox.
	Update(ctx context.Context, in *UpdateSandboxRequest, opts ...grpc.CallOption) (*UpdateSandboxResponse, error)
	// AppendContainer append a new container to a sandbox, all the resources like cpu/mem/devices/mounts,
	// will be attached to the sandbox.
	AppendContainer(ctx context.Context, in *AppendContainerRequest, opts ...grpc.CallOption) (*AppendContainerResponse, error)
	// UpdateContainer update a container in a sandbox, the resources belongs to the container maybe changed.
	// for example, the k8s update container cpu/mem limit of a container, sandbox should be aware of it,
	// and adjust the resources it is managing. another case of calling UpdateContainer is when
	// execing a process in a container, the io pipes should be attached to the sandbox.
	UpdateContainer(ctx context.Context, in *UpdateContainerRequest, opts ...grpc.CallOption) (*UpdateContainerResponse, error)
	// RemoveContainer removes all resources belongs to a container in a sandbox.
	RemoveContainer(ctx context.Context, in *RemoveContainerRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	// Delete delete a sandbox, there is no Stop API in sandboxer, so delete a sandbox also stopped it.
	// not only metadata deleted from db, but the running sandbox should be stopped, and all resources should be released.
	Delete(ctx context.Context, in *DeleteSandboxRequest, opts ...grpc.CallOption) (*emptypb.Empty, error)
	// List list all sandboxes belongs to a specific sandboxer.
	List(ctx context.Context, in *ListSandboxRequest, opts ...grpc.CallOption) (*ListSandboxResponse, error)
	// Get gets a sandbox metadata from db.
	Get(ctx context.Context, in *GetSandboxRequest, opts ...grpc.CallOption) (*GetSandboxResponse, error)
	// Status query the status of a sandbox, check if it is running or stopped of paused .etc.
	Status(ctx context.Context, in *StatusRequest, opts ...grpc.CallOption) (*StatusResponse, error)
}

type sandboxerClient struct {
	cc grpc.ClientConnInterface
}

func NewSandboxerClient(cc grpc.ClientConnInterface) SandboxerClient {
	return &sandboxerClient{cc}
}

func (c *sandboxerClient) Create(ctx context.Context, in *CreateSandboxRequest, opts ...grpc.CallOption) (*CreateSandboxResponse, error) {
	out := new(CreateSandboxResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/Create", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) Update(ctx context.Context, in *UpdateSandboxRequest, opts ...grpc.CallOption) (*UpdateSandboxResponse, error) {
	out := new(UpdateSandboxResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/Update", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) AppendContainer(ctx context.Context, in *AppendContainerRequest, opts ...grpc.CallOption) (*AppendContainerResponse, error) {
	out := new(AppendContainerResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/AppendContainer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) UpdateContainer(ctx context.Context, in *UpdateContainerRequest, opts ...grpc.CallOption) (*UpdateContainerResponse, error) {
	out := new(UpdateContainerResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/UpdateContainer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) RemoveContainer(ctx context.Context, in *RemoveContainerRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/RemoveContainer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) Delete(ctx context.Context, in *DeleteSandboxRequest, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	out := new(emptypb.Empty)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/Delete", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) List(ctx context.Context, in *ListSandboxRequest, opts ...grpc.CallOption) (*ListSandboxResponse, error) {
	out := new(ListSandboxResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/List", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) Get(ctx context.Context, in *GetSandboxRequest, opts ...grpc.CallOption) (*GetSandboxResponse, error) {
	out := new(GetSandboxResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/Get", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *sandboxerClient) Status(ctx context.Context, in *StatusRequest, opts ...grpc.CallOption) (*StatusResponse, error) {
	out := new(StatusResponse)
	err := c.cc.Invoke(ctx, "/containerd.services.sandbox.v1.Sandboxer/Status", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SandboxerServer is the server API for Sandboxer service.
// All implementations must embed UnimplementedSandboxerServer
// for forward compatibility
type SandboxerServer interface {
	// Create create a new sandbox, as there is only "Create" and "Delete" in sandbox lifecycle,
	// after creation, the sandbox should be running.
	Create(context.Context, *CreateSandboxRequest) (*CreateSandboxResponse, error)
	// Update update a sandbox metadata, it only updates spec/labels/extensions of this sandbox.
	Update(context.Context, *UpdateSandboxRequest) (*UpdateSandboxResponse, error)
	// AppendContainer append a new container to a sandbox, all the resources like cpu/mem/devices/mounts,
	// will be attached to the sandbox.
	AppendContainer(context.Context, *AppendContainerRequest) (*AppendContainerResponse, error)
	// UpdateContainer update a container in a sandbox, the resources belongs to the container maybe changed.
	// for example, the k8s update container cpu/mem limit of a container, sandbox should be aware of it,
	// and adjust the resources it is managing. another case of calling UpdateContainer is when
	// execing a process in a container, the io pipes should be attached to the sandbox.
	UpdateContainer(context.Context, *UpdateContainerRequest) (*UpdateContainerResponse, error)
	// RemoveContainer removes all resources belongs to a container in a sandbox.
	RemoveContainer(context.Context, *RemoveContainerRequest) (*emptypb.Empty, error)
	// Delete delete a sandbox, there is no Stop API in sandboxer, so delete a sandbox also stopped it.
	// not only metadata deleted from db, but the running sandbox should be stopped, and all resources should be released.
	Delete(context.Context, *DeleteSandboxRequest) (*emptypb.Empty, error)
	// List list all sandboxes belongs to a specific sandboxer.
	List(context.Context, *ListSandboxRequest) (*ListSandboxResponse, error)
	// Get gets a sandbox metadata from db.
	Get(context.Context, *GetSandboxRequest) (*GetSandboxResponse, error)
	// Status query the status of a sandbox, check if it is running or stopped of paused .etc.
	Status(context.Context, *StatusRequest) (*StatusResponse, error)
	mustEmbedUnimplementedSandboxerServer()
}

// UnimplementedSandboxerServer must be embedded to have forward compatible implementations.
type UnimplementedSandboxerServer struct {
}

func (UnimplementedSandboxerServer) Create(context.Context, *CreateSandboxRequest) (*CreateSandboxResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Create not implemented")
}
func (UnimplementedSandboxerServer) Update(context.Context, *UpdateSandboxRequest) (*UpdateSandboxResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Update not implemented")
}
func (UnimplementedSandboxerServer) AppendContainer(context.Context, *AppendContainerRequest) (*AppendContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AppendContainer not implemented")
}
func (UnimplementedSandboxerServer) UpdateContainer(context.Context, *UpdateContainerRequest) (*UpdateContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdateContainer not implemented")
}
func (UnimplementedSandboxerServer) RemoveContainer(context.Context, *RemoveContainerRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method RemoveContainer not implemented")
}
func (UnimplementedSandboxerServer) Delete(context.Context, *DeleteSandboxRequest) (*emptypb.Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Delete not implemented")
}
func (UnimplementedSandboxerServer) List(context.Context, *ListSandboxRequest) (*ListSandboxResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method List not implemented")
}
func (UnimplementedSandboxerServer) Get(context.Context, *GetSandboxRequest) (*GetSandboxResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Get not implemented")
}
func (UnimplementedSandboxerServer) Status(context.Context, *StatusRequest) (*StatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Status not implemented")
}
func (UnimplementedSandboxerServer) mustEmbedUnimplementedSandboxerServer() {}

// UnsafeSandboxerServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to SandboxerServer will
// result in compilation errors.
type UnsafeSandboxerServer interface {
	mustEmbedUnimplementedSandboxerServer()
}

func RegisterSandboxerServer(s grpc.ServiceRegistrar, srv SandboxerServer) {
	s.RegisterService(&Sandboxer_ServiceDesc, srv)
}

func _Sandboxer_Create_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateSandboxRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).Create(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/Create",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).Create(ctx, req.(*CreateSandboxRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_Update_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateSandboxRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).Update(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/Update",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).Update(ctx, req.(*UpdateSandboxRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_AppendContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(AppendContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).AppendContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/AppendContainer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).AppendContainer(ctx, req.(*AppendContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_UpdateContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).UpdateContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/UpdateContainer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).UpdateContainer(ctx, req.(*UpdateContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_RemoveContainer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RemoveContainerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).RemoveContainer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/RemoveContainer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).RemoveContainer(ctx, req.(*RemoveContainerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_Delete_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteSandboxRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).Delete(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/Delete",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).Delete(ctx, req.(*DeleteSandboxRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_List_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListSandboxRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).List(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/List",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).List(ctx, req.(*ListSandboxRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_Get_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetSandboxRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).Get(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/Get",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).Get(ctx, req.(*GetSandboxRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Sandboxer_Status_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(SandboxerServer).Status(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/containerd.services.sandbox.v1.Sandboxer/Status",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(SandboxerServer).Status(ctx, req.(*StatusRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// Sandboxer_ServiceDesc is the grpc.ServiceDesc for Sandboxer service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Sandboxer_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "containerd.services.sandbox.v1.Sandboxer",
	HandlerType: (*SandboxerServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Create",
			Handler:    _Sandboxer_Create_Handler,
		},
		{
			MethodName: "Update",
			Handler:    _Sandboxer_Update_Handler,
		},
		{
			MethodName: "AppendContainer",
			Handler:    _Sandboxer_AppendContainer_Handler,
		},
		{
			MethodName: "UpdateContainer",
			Handler:    _Sandboxer_UpdateContainer_Handler,
		},
		{
			MethodName: "RemoveContainer",
			Handler:    _Sandboxer_RemoveContainer_Handler,
		},
		{
			MethodName: "Delete",
			Handler:    _Sandboxer_Delete_Handler,
		},
		{
			MethodName: "List",
			Handler:    _Sandboxer_List_Handler,
		},
		{
			MethodName: "Get",
			Handler:    _Sandboxer_Get_Handler,
		},
		{
			MethodName: "Status",
			Handler:    _Sandboxer_Status_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "github.com/containerd/containerd/api/services/sandbox/v1/sandbox.proto",
}
