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

package testing

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/execution"
	"github.com/containerd/containerd/api/types/container"
	googleprotobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var containerNotExistError = grpc.Errorf(codes.Unknown, containerd.ErrContainerNotExist.Error())

// CalledDetail is the struct contains called function name and arguments.
type CalledDetail struct {
	// Name of the function called.
	Name string
	// Argument of the function called.
	Argument interface{}
}

var _ execution.ContainerService_EventsClient = &EventClient{}

// EventClient is a test implementation of execution.ContainerService_EventsClient
type EventClient struct {
	Events chan *container.Event
	grpc.ClientStream
}

// Recv is a test implementation of Recv
func (cli *EventClient) Recv() (*container.Event, error) {
	event, ok := <-cli.Events
	if !ok {
		return nil, fmt.Errorf("event channel closed")
	}
	return event, nil
}

// FakeExecutionClient is a simple fake execution client, so that cri-containerd
// can be run for testing without requiring a real containerd setup.
type FakeExecutionClient struct {
	sync.Mutex
	called        []CalledDetail
	errors        map[string]error
	ContainerList map[string]container.Container
	eventsQueue   chan *container.Event
	eventClients  []*EventClient
}

var _ execution.ContainerServiceClient = &FakeExecutionClient{}

// NewFakeExecutionClient creates a FakeExecutionClient
func NewFakeExecutionClient() *FakeExecutionClient {
	return &FakeExecutionClient{
		errors:        make(map[string]error),
		ContainerList: make(map[string]container.Container),
	}
}

// Stop the fake execution service. Needed when event is enabled.
func (f *FakeExecutionClient) Stop() {
	if f.eventsQueue != nil {
		close(f.eventsQueue)
	}
	f.Lock()
	defer f.Unlock()
	for _, client := range f.eventClients {
		close(client.Events)
	}
}

// WithEvents setup events publisher for FakeExecutionClient
func (f *FakeExecutionClient) WithEvents() *FakeExecutionClient {
	f.eventsQueue = make(chan *container.Event, 1024)
	go func() {
		for e := range f.eventsQueue {
			f.Lock()
			for _, client := range f.eventClients {
				client.Events <- e
			}
			f.Unlock()
		}
	}()
	return f
}

func (f *FakeExecutionClient) getError(op string) error {
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeExecutionClient) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeExecutionClient) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeExecutionClient) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

func generatePid() uint32 {
	rand.Seed(time.Now().Unix())
	randPid := uint32(rand.Intn(1000))
	return randPid
}

func (f *FakeExecutionClient) sendEvent(event *container.Event) {
	if f.eventsQueue != nil {
		f.eventsQueue <- event
	}
}

func (f *FakeExecutionClient) appendCalled(name string, argument interface{}) {
	call := CalledDetail{Name: name, Argument: argument}
	f.called = append(f.called, call)
}

// GetCalledNames get names of call
func (f *FakeExecutionClient) GetCalledNames() []string {
	f.Lock()
	defer f.Unlock()
	names := []string{}
	for _, detail := range f.called {
		names = append(names, detail.Name)
	}
	return names
}

// ClearCalls clear all call detail.
func (f *FakeExecutionClient) ClearCalls() {
	f.Lock()
	defer f.Unlock()
	f.called = []CalledDetail{}
}

// GetCalledDetails get detail of each call.
func (f *FakeExecutionClient) GetCalledDetails() []CalledDetail {
	f.Lock()
	defer f.Unlock()
	// Copy the list and return.
	return append([]CalledDetail{}, f.called...)
}

// SetFakeContainers injects fake containers.
func (f *FakeExecutionClient) SetFakeContainers(containers []container.Container) {
	f.Lock()
	defer f.Unlock()
	for _, c := range containers {
		f.ContainerList[c.ID] = c
	}
}

// Create is a test implementation of execution.Create.
func (f *FakeExecutionClient) Create(ctx context.Context, createOpts *execution.CreateRequest, opts ...grpc.CallOption) (*execution.CreateResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("create", createOpts)
	if err := f.getError("create"); err != nil {
		return nil, err
	}
	_, ok := f.ContainerList[createOpts.ID]
	if ok {
		return nil, containerd.ErrContainerExists
	}
	pid := generatePid()
	f.ContainerList[createOpts.ID] = container.Container{
		ID:     createOpts.ID,
		Pid:    pid,
		Status: container.Status_CREATED,
	}
	f.sendEvent(&container.Event{
		ID:   createOpts.ID,
		Type: container.Event_CREATE,
		Pid:  pid,
	})
	return &execution.CreateResponse{
		ID:  createOpts.ID,
		Pid: pid,
	}, nil
}

// Start is a test implementation of execution.Start
func (f *FakeExecutionClient) Start(ctx context.Context, startOpts *execution.StartRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("start", startOpts)
	if err := f.getError("start"); err != nil {
		return nil, err
	}
	c, ok := f.ContainerList[startOpts.ID]
	if !ok {
		return nil, containerNotExistError
	}
	f.sendEvent(&container.Event{
		ID:   c.ID,
		Type: container.Event_START,
		Pid:  c.Pid,
	})
	switch c.Status {
	case container.Status_CREATED:
		c.Status = container.Status_RUNNING
		f.ContainerList[startOpts.ID] = c
		return &googleprotobuf.Empty{}, nil
	case container.Status_STOPPED:
		return &googleprotobuf.Empty{}, fmt.Errorf("cannot start a container that has stopped")
	case container.Status_RUNNING:
		return &googleprotobuf.Empty{}, fmt.Errorf("cannot start an already running container")
	default:
		return &googleprotobuf.Empty{}, fmt.Errorf("cannot start a container in the %s state", c.Status)
	}
}

// Delete is a test implementation of execution.Delete
func (f *FakeExecutionClient) Delete(ctx context.Context, deleteOpts *execution.DeleteRequest, opts ...grpc.CallOption) (*execution.DeleteResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("delete", deleteOpts)
	if err := f.getError("delete"); err != nil {
		return nil, err
	}
	c, ok := f.ContainerList[deleteOpts.ID]
	if !ok {
		return nil, containerNotExistError
	}
	delete(f.ContainerList, deleteOpts.ID)
	f.sendEvent(&container.Event{
		ID:   c.ID,
		Type: container.Event_EXIT,
		Pid:  c.Pid,
	})
	return nil, nil
}

// Info is a test implementation of execution.Info
func (f *FakeExecutionClient) Info(ctx context.Context, infoOpts *execution.InfoRequest, opts ...grpc.CallOption) (*container.Container, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("info", infoOpts)
	if err := f.getError("info"); err != nil {
		return nil, err
	}
	c, ok := f.ContainerList[infoOpts.ID]
	if !ok {
		return nil, containerNotExistError
	}
	return &c, nil
}

// List is a test implementation of execution.List
func (f *FakeExecutionClient) List(ctx context.Context, listOpts *execution.ListRequest, opts ...grpc.CallOption) (*execution.ListResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("list", listOpts)
	if err := f.getError("list"); err != nil {
		return nil, err
	}
	resp := &execution.ListResponse{}
	for _, c := range f.ContainerList {
		resp.Containers = append(resp.Containers, &container.Container{
			ID:     c.ID,
			Pid:    c.Pid,
			Status: c.Status,
		})
	}
	return resp, nil
}

// Kill is a test implementation of execution.Kill
func (f *FakeExecutionClient) Kill(ctx context.Context, killOpts *execution.KillRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("kill", killOpts)
	if err := f.getError("kill"); err != nil {
		return nil, err
	}
	c, ok := f.ContainerList[killOpts.ID]
	if !ok {
		return nil, containerNotExistError
	}
	c.Status = container.Status_STOPPED
	f.ContainerList[killOpts.ID] = c
	f.sendEvent(&container.Event{
		ID:   c.ID,
		Type: container.Event_EXIT,
		Pid:  c.Pid,
	})
	return &googleprotobuf.Empty{}, nil
}

// Events is a test implementation of execution.Events
func (f *FakeExecutionClient) Events(ctx context.Context, eventsOpts *execution.EventsRequest, opts ...grpc.CallOption) (execution.ContainerService_EventsClient, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("events", eventsOpts)
	if err := f.getError("events"); err != nil {
		return nil, err
	}
	var client = &EventClient{
		Events: make(chan *container.Event, 100),
	}
	f.eventClients = append(f.eventClients, client)
	return client, nil
}

// Exec is a test implementation of execution.Exec
func (f *FakeExecutionClient) Exec(ctx context.Context, execOpts *execution.ExecRequest, opts ...grpc.CallOption) (*execution.ExecResponse, error) {
	// TODO: implement Exec()
	return nil, nil
}

// Pty is a test implementation of execution.Pty
func (f *FakeExecutionClient) Pty(ctx context.Context, ptyOpts *execution.PtyRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	// TODO: implement Pty()
	return nil, nil
}

// CloseStdin is a test implementation of execution.CloseStdin
func (f *FakeExecutionClient) CloseStdin(ctx context.Context, closeStdinOpts *execution.CloseStdinRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	// TODO: implement CloseStdin()
	return nil, nil
}

// Pause is a test implementation of execution.Pause
func (f *FakeExecutionClient) Pause(ctx context.Context, in *execution.PauseRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	// TODO: implement Pause()
	return nil, nil
}

// Resume is a test implementation of execution.Resume
func (f *FakeExecutionClient) Resume(ctx context.Context, in *execution.ResumeRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	// TODO: implement Resume()
	return nil, nil
}
