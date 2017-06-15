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
	"sync"

	"github.com/containerd/containerd/api/services/containers"
	googleprotobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// ContainerNotExistError is the fake error returned when container does not exist.
var ContainerNotExistError = grpc.Errorf(codes.NotFound, "container does not exist")

// FakeContainersClient is a simple fake containers client, so that cri-containerd
// can be run for testing without requiring a real containerd setup.
type FakeContainersClient struct {
	sync.Mutex
	called        []CalledDetail
	errors        map[string]error
	ContainerList map[string]containers.Container
}

var _ containers.ContainersClient = &FakeContainersClient{}

// NewFakeContainersClient creates a FakeContainersClient
func NewFakeContainersClient() *FakeContainersClient {
	return &FakeContainersClient{
		errors:        make(map[string]error),
		ContainerList: make(map[string]containers.Container),
	}
}

func (f *FakeContainersClient) getError(op string) error {
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeContainersClient) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeContainersClient) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeContainersClient) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

func (f *FakeContainersClient) appendCalled(name string, argument interface{}) {
	call := CalledDetail{Name: name, Argument: argument}
	f.called = append(f.called, call)
}

// GetCalledNames get names of call
func (f *FakeContainersClient) GetCalledNames() []string {
	f.Lock()
	defer f.Unlock()
	names := []string{}
	for _, detail := range f.called {
		names = append(names, detail.Name)
	}
	return names
}

// ClearCalls clear all call detail.
func (f *FakeContainersClient) ClearCalls() {
	f.Lock()
	defer f.Unlock()
	f.called = []CalledDetail{}
}

// GetCalledDetails get detail of each call.
func (f *FakeContainersClient) GetCalledDetails() []CalledDetail {
	f.Lock()
	defer f.Unlock()
	// Copy the list and return.
	return append([]CalledDetail{}, f.called...)
}

// Create is a test implementation of containers.Create.
func (f *FakeContainersClient) Create(ctx context.Context, createOpts *containers.CreateContainerRequest, opts ...grpc.CallOption) (*containers.CreateContainerResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("create", createOpts)
	if err := f.getError("create"); err != nil {
		return nil, err
	}
	_, ok := f.ContainerList[createOpts.Container.ID]
	if ok {
		return nil, fmt.Errorf("container already exists")
	}
	f.ContainerList[createOpts.Container.ID] = createOpts.Container
	return &containers.CreateContainerResponse{Container: createOpts.Container}, nil
}

// Delete is a test implementation of containers.Delete
func (f *FakeContainersClient) Delete(ctx context.Context, deleteOpts *containers.DeleteContainerRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("delete", deleteOpts)
	if err := f.getError("delete"); err != nil {
		return nil, err
	}
	_, ok := f.ContainerList[deleteOpts.ID]
	if !ok {
		return nil, ContainerNotExistError
	}
	delete(f.ContainerList, deleteOpts.ID)
	return &googleprotobuf.Empty{}, nil
}

// Get is a test implementation of containers.Get
func (f *FakeContainersClient) Get(ctx context.Context, getOpts *containers.GetContainerRequest, opts ...grpc.CallOption) (*containers.GetContainerResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("get", getOpts)
	if err := f.getError("get"); err != nil {
		return nil, err
	}
	c, ok := f.ContainerList[getOpts.ID]
	if !ok {
		return nil, ContainerNotExistError
	}
	return &containers.GetContainerResponse{Container: c}, nil
}

// List is a test implementation of containers.List
func (f *FakeContainersClient) List(ctx context.Context, listOpts *containers.ListContainersRequest, opts ...grpc.CallOption) (*containers.ListContainersResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("list", listOpts)
	if err := f.getError("list"); err != nil {
		return nil, err
	}
	var cs []containers.Container
	for _, c := range f.ContainerList {
		cs = append(cs, c)
	}
	return &containers.ListContainersResponse{Containers: cs}, nil
}

// Update is a test implementation of containers.Update
func (f *FakeContainersClient) Update(ctx context.Context, updateOpts *containers.UpdateContainerRequest, opts ...grpc.CallOption) (*containers.UpdateContainerResponse, error) {
	// TODO: implement Update()
	return nil, nil
}
