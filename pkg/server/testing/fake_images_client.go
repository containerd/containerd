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

	"github.com/containerd/containerd/api/services/images"
	googleprotobuf "github.com/golang/protobuf/ptypes/empty"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// FakeImagesClient is a simple fake images client, so that cri-containerd
// can be run for testing without requiring a real containerd setup.
type FakeImagesClient struct {
	sync.Mutex
	called    []CalledDetail
	errors    map[string]error
	ImageList map[string]images.Image
}

var _ images.ImagesClient = &FakeImagesClient{}

// NewFakeImagesClient creates a FakeImagesClient
func NewFakeImagesClient() *FakeImagesClient {
	return &FakeImagesClient{
		errors:    make(map[string]error),
		ImageList: make(map[string]images.Image),
	}
}

// getError get error for call
func (f *FakeImagesClient) getError(op string) error {
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeImagesClient) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeImagesClient) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeImagesClient) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

func (f *FakeImagesClient) appendCalled(name string, argument interface{}) {
	call := CalledDetail{Name: name, Argument: argument}
	f.called = append(f.called, call)
}

// GetCalledNames get names of call
func (f *FakeImagesClient) GetCalledNames() []string {
	f.Lock()
	defer f.Unlock()
	names := []string{}
	for _, detail := range f.called {
		names = append(names, detail.Name)
	}
	return names
}

// SetFakeImages injects fake images.
func (f *FakeImagesClient) SetFakeImages(images []images.Image) {
	f.Lock()
	defer f.Unlock()
	for _, image := range images {
		f.ImageList[image.Name] = image
	}
}

// Get is a test implementation of images.Get
func (f *FakeImagesClient) Get(ctx context.Context, getOpts *images.GetRequest, opts ...grpc.CallOption) (*images.GetResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("get", getOpts)
	if err := f.getError("get"); err != nil {
		return nil, err
	}
	image, ok := f.ImageList[getOpts.Name]
	if !ok {
		return nil, fmt.Errorf("image does not exist")
	}
	return &images.GetResponse{
		Image: &image,
	}, nil
}

// Put is a test implementation of images.Put
func (f *FakeImagesClient) Put(ctx context.Context, putOpts *images.PutRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("put", putOpts)
	if err := f.getError("put"); err != nil {
		return nil, err
	}
	f.ImageList[putOpts.Image.Name] = putOpts.Image
	return &googleprotobuf.Empty{}, nil
}

// List is a test implementation of images.List
func (f *FakeImagesClient) List(ctx context.Context, listOpts *images.ListRequest, opts ...grpc.CallOption) (*images.ListResponse, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("list", listOpts)
	if err := f.getError("list"); err != nil {
		return nil, err
	}
	resp := &images.ListResponse{}
	for _, image := range f.ImageList {
		resp.Images = append(resp.Images, image)
	}
	return resp, nil
}

// Delete is a test implementation of images.Delete
func (f *FakeImagesClient) Delete(ctx context.Context, deleteOpts *images.DeleteRequest, opts ...grpc.CallOption) (*googleprotobuf.Empty, error) {
	f.Lock()
	defer f.Unlock()
	f.appendCalled("delete", deleteOpts)
	if err := f.getError("delete"); err != nil {
		return nil, err
	}
	_, ok := f.ImageList[deleteOpts.Name]
	if !ok {
		return nil, fmt.Errorf("image does not exist")
	}
	delete(f.ImageList, deleteOpts.Name)
	return &googleprotobuf.Empty{}, nil
}
