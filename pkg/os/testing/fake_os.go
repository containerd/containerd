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
	"io"
	"os"

	"golang.org/x/net/context"

	osInterface "github.com/kubernetes-incubator/cri-containerd/pkg/os"
)

// FakeOS mocks out certain OS calls to avoid perturbing the filesystem
// If a member of the form `*Fn` is set, that function will be called in place
// of the real call.
type FakeOS struct {
	MkdirAllFn  func(string, os.FileMode) error
	RemoveAllFn func(string) error
	OpenFifoFn  func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error)
}

var _ osInterface.OS = &FakeOS{}

// NewFakeOS creates a FakeOS.
func NewFakeOS() *FakeOS {
	return &FakeOS{}
}

// MkdirAll is a fake call that invokes MkdirAllFn or just returns nil.
func (f *FakeOS) MkdirAll(path string, perm os.FileMode) error {
	if f.MkdirAllFn != nil {
		return f.MkdirAllFn(path, perm)
	}
	return nil
}

// RemoveAll is a fake call that invokes RemoveAllFn or just returns nil.
func (f *FakeOS) RemoveAll(path string) error {
	if f.RemoveAllFn != nil {
		return f.RemoveAllFn(path)
	}
	return nil
}

// OpenFifo is a fake call that invokes OpenFifoFn or just returns nil.
func (f *FakeOS) OpenFifo(ctx context.Context, fn string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if f.OpenFifoFn != nil {
		return f.OpenFifoFn(ctx, fn, flag, perm)
	}
	return nil, nil
}
