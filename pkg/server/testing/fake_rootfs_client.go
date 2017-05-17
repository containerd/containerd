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

	"github.com/containerd/containerd/api/services/rootfs"
	"github.com/containerd/containerd/api/types/descriptor"
	"github.com/containerd/containerd/api/types/mount"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// FakeRootfsClient is a simple fake rootfs client, so that cri-containerd
// can be run for testing without requiring a real containerd setup.
type FakeRootfsClient struct {
	sync.Mutex
	called      []CalledDetail
	errors      map[string]error
	ChainIDList map[digest.Digest]struct{}
	MountList   map[string][]*mount.Mount
}

var _ rootfs.RootFSClient = &FakeRootfsClient{}

// NewFakeRootfsClient creates a FakeRootfsClient
func NewFakeRootfsClient() *FakeRootfsClient {
	return &FakeRootfsClient{
		errors:      make(map[string]error),
		ChainIDList: make(map[digest.Digest]struct{}),
		MountList:   make(map[string][]*mount.Mount),
	}
}

func (f *FakeRootfsClient) popError(op string) error {
	if f.errors == nil {
		return nil
	}
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeRootfsClient) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeRootfsClient) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeRootfsClient) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

//For uncompressed layers, diffID and digest will be the same. For compressed
//layers, we can look up the diffID from the digest if we've already unpacked it.
//In the FakeRootfsClient, We just use layer digest as diffID.
func generateChainID(layers []*descriptor.Descriptor) digest.Digest {
	var digests []digest.Digest
	for _, layer := range layers {
		digests = append(digests, layer.Digest)
	}
	parent := identity.ChainID(digests)
	return parent
}

func (f *FakeRootfsClient) appendCalled(name string, argument interface{}) {
	call := CalledDetail{Name: name, Argument: argument}
	f.called = append(f.called, call)
}

// GetCalledNames get names of call
func (f *FakeRootfsClient) GetCalledNames() []string {
	f.Lock()
	defer f.Unlock()
	names := []string{}
	for _, detail := range f.called {
		names = append(names, detail.Name)
	}
	return names
}

// GetCalledDetails get detail of each call.
func (f *FakeRootfsClient) GetCalledDetails() []CalledDetail {
	f.Lock()
	defer f.Unlock()
	// Copy the list and return.
	return append([]CalledDetail{}, f.called...)
}

// SetFakeChainIDs injects fake chainIDs.
func (f *FakeRootfsClient) SetFakeChainIDs(chainIDs []digest.Digest) {
	f.Lock()
	defer f.Unlock()
	for _, c := range chainIDs {
		f.ChainIDList[c] = struct{}{}
	}
}

// SetFakeMounts injects fake mounts.
func (f *FakeRootfsClient) SetFakeMounts(name string, mounts []*mount.Mount) {
	f.Lock()
	defer f.Unlock()
	f.MountList[name] = mounts
}

// Unpack is a test implementation of rootfs.Unpack
func (f *FakeRootfsClient) Unpack(ctx context.Context, unpackOpts *rootfs.UnpackRequest, opts ...grpc.CallOption) (*rootfs.UnpackResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("unpack", unpackOpts)
	if err := f.popError("unpack"); err != nil {
		return nil, err
	}
	chainID := generateChainID(unpackOpts.Layers)
	_, ok := f.ChainIDList[chainID]
	if ok {
		return nil, fmt.Errorf("already unpacked")
	}
	f.ChainIDList[chainID] = struct{}{}
	return &rootfs.UnpackResponse{
		ChainID: chainID,
	}, nil
}

// Prepare is a test implementation of rootfs.Prepare
func (f *FakeRootfsClient) Prepare(ctx context.Context, prepareOpts *rootfs.PrepareRequest, opts ...grpc.CallOption) (*rootfs.MountResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("prepare", prepareOpts)
	if err := f.popError("prepare"); err != nil {
		return nil, err
	}
	_, ok := f.ChainIDList[prepareOpts.ChainID]
	if !ok {
		return nil, fmt.Errorf("have not been unpacked")
	}
	_, ok = f.MountList[prepareOpts.Name]
	if ok {
		return nil, fmt.Errorf("mounts already exist")
	}
	f.MountList[prepareOpts.Name] = []*mount.Mount{}
	return &rootfs.MountResponse{
		Mounts: f.MountList[prepareOpts.Name],
	}, nil
}

// Mounts is a test implementation of rootfs.Mounts
func (f *FakeRootfsClient) Mounts(ctx context.Context, mountsOpts *rootfs.MountsRequest, opts ...grpc.CallOption) (*rootfs.MountResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("mounts", mountsOpts)
	if err := f.popError("mounts"); err != nil {
		return nil, err
	}
	mounts, ok := f.MountList[mountsOpts.Name]
	if !ok {
		return nil, fmt.Errorf("mounts not exist")
	}
	return &rootfs.MountResponse{
		Mounts: mounts,
	}, nil
}
