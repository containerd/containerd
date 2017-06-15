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

	"github.com/containerd/containerd/api/services/snapshot"
	"github.com/containerd/containerd/api/types/mount"
	google_protobuf1 "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// SnapshotNotExistError is the fake error returned when snapshot does not exist.
var SnapshotNotExistError = grpc.Errorf(codes.NotFound, "snapshot does not exist")

// FakeSnapshotClient is a simple fake snapshot client, so that cri-containerd
// can be run for testing without requiring a real containerd setup.
type FakeSnapshotClient struct {
	sync.Mutex
	called    []CalledDetail
	errors    map[string]error
	MountList map[string][]*mount.Mount
}

var _ snapshot.SnapshotClient = &FakeSnapshotClient{}

// NewFakeSnapshotClient creates a FakeSnapshotClient
func NewFakeSnapshotClient() *FakeSnapshotClient {
	return &FakeSnapshotClient{
		errors:    make(map[string]error),
		MountList: make(map[string][]*mount.Mount),
	}
}

func (f *FakeSnapshotClient) getError(op string) error {
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeSnapshotClient) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeSnapshotClient) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeSnapshotClient) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

func (f *FakeSnapshotClient) appendCalled(name string, argument interface{}) {
	call := CalledDetail{Name: name, Argument: argument}
	f.called = append(f.called, call)
}

// GetCalledNames get names of call
func (f *FakeSnapshotClient) GetCalledNames() []string {
	f.Lock()
	defer f.Unlock()
	names := []string{}
	for _, detail := range f.called {
		names = append(names, detail.Name)
	}
	return names
}

// GetCalledDetails get detail of each call.
func (f *FakeSnapshotClient) GetCalledDetails() []CalledDetail {
	f.Lock()
	defer f.Unlock()
	// Copy the list and return.
	return append([]CalledDetail{}, f.called...)
}

// SetFakeMounts injects fake mounts.
func (f *FakeSnapshotClient) SetFakeMounts(name string, mounts []*mount.Mount) {
	f.Lock()
	defer f.Unlock()
	f.MountList[name] = mounts
}

// ListMounts lists all the fake mounts.
func (f *FakeSnapshotClient) ListMounts() [][]*mount.Mount {
	f.Lock()
	defer f.Unlock()
	var ms [][]*mount.Mount
	for _, m := range f.MountList {
		ms = append(ms, m)
	}
	return ms
}

// Prepare is a test implementation of snapshot.Prepare
func (f *FakeSnapshotClient) Prepare(ctx context.Context, prepareOpts *snapshot.PrepareRequest, opts ...grpc.CallOption) (*snapshot.MountsResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("prepare", prepareOpts)
	if err := f.getError("prepare"); err != nil {
		return nil, err
	}
	_, ok := f.MountList[prepareOpts.Key]
	if ok {
		return nil, fmt.Errorf("mounts already exist")
	}
	f.MountList[prepareOpts.Key] = []*mount.Mount{{
		Type:   "bind",
		Source: prepareOpts.Key,
		// TODO(random-liu): Fake options based on Readonly option.
	}}
	return &snapshot.MountsResponse{
		Mounts: f.MountList[prepareOpts.Key],
	}, nil
}

// Mounts is a test implementation of snapshot.Mounts
func (f *FakeSnapshotClient) Mounts(ctx context.Context, mountsOpts *snapshot.MountsRequest, opts ...grpc.CallOption) (*snapshot.MountsResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("mounts", mountsOpts)
	if err := f.getError("mounts"); err != nil {
		return nil, err
	}
	mounts, ok := f.MountList[mountsOpts.Key]
	if !ok {
		return nil, SnapshotNotExistError
	}
	return &snapshot.MountsResponse{
		Mounts: mounts,
	}, nil
}

// Commit is a test implementation of snapshot.Commit
func (f *FakeSnapshotClient) Commit(ctx context.Context, in *snapshot.CommitRequest, opts ...grpc.CallOption) (*google_protobuf1.Empty, error) {
	return nil, nil
}

// View is a test implementation of snapshot.View
func (f *FakeSnapshotClient) View(ctx context.Context, viewOpts *snapshot.PrepareRequest, opts ...grpc.CallOption) (*snapshot.MountsResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("view", viewOpts)
	if err := f.getError("view"); err != nil {
		return nil, err
	}
	_, ok := f.MountList[viewOpts.Key]
	if ok {
		return nil, fmt.Errorf("mounts already exist")
	}
	f.MountList[viewOpts.Key] = []*mount.Mount{{
		Type:   "bind",
		Source: viewOpts.Key,
		// TODO(random-liu): Fake options based on Readonly option.
	}}
	return &snapshot.MountsResponse{
		Mounts: f.MountList[viewOpts.Key],
	}, nil
}

// Remove is a test implementation of snapshot.Remove
func (f *FakeSnapshotClient) Remove(ctx context.Context, removeOpts *snapshot.RemoveRequest, opts ...grpc.CallOption) (*google_protobuf1.Empty, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("remove", removeOpts)
	if err := f.getError("remove"); err != nil {
		return nil, err
	}
	if _, ok := f.MountList[removeOpts.Key]; !ok {
		return nil, SnapshotNotExistError
	}
	delete(f.MountList, removeOpts.Key)
	return &google_protobuf1.Empty{}, nil
}

// Stat is a test implementation of snapshot.Stat
func (f *FakeSnapshotClient) Stat(ctx context.Context, in *snapshot.StatRequest, opts ...grpc.CallOption) (*snapshot.StatResponse, error) {
	return nil, nil
}

// List is a test implementation of snapshot.List
func (f *FakeSnapshotClient) List(ctx context.Context, in *snapshot.ListRequest, opts ...grpc.CallOption) (snapshot.Snapshot_ListClient, error) {
	return nil, nil
}

// Usage is a test implementation of snapshot.Usage
func (f *FakeSnapshotClient) Usage(ctx context.Context, in *snapshot.UsageRequest, opts ...grpc.CallOption) (*snapshot.UsageResponse, error) {
	return nil, nil
}
