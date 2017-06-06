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
	"sync"

	"golang.org/x/net/context"

	osInterface "github.com/kubernetes-incubator/cri-containerd/pkg/os"
)

// FakeOS mocks out certain OS calls to avoid perturbing the filesystem
// If a member of the form `*Fn` is set, that function will be called in place
// of the real call.
type FakeOS struct {
	sync.Mutex
	MkdirAllFn  func(string, os.FileMode) error
	RemoveAllFn func(string) error
	OpenFifoFn  func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error)
	StatFn      func(string) (os.FileInfo, error)
	CopyFileFn  func(string, string, os.FileMode) error
	errors      map[string]error
}

var _ osInterface.OS = &FakeOS{}

// getError get error for call
func (f *FakeOS) getError(op string) error {
	f.Lock()
	defer f.Unlock()
	err, ok := f.errors[op]
	if ok {
		delete(f.errors, op)
		return err
	}
	return nil
}

// InjectError inject error for call
func (f *FakeOS) InjectError(fn string, err error) {
	f.Lock()
	defer f.Unlock()
	f.errors[fn] = err
}

// InjectErrors inject errors for calls
func (f *FakeOS) InjectErrors(errs map[string]error) {
	f.Lock()
	defer f.Unlock()
	for fn, err := range errs {
		f.errors[fn] = err
	}
}

// ClearErrors clear errors for call
func (f *FakeOS) ClearErrors() {
	f.Lock()
	defer f.Unlock()
	f.errors = make(map[string]error)
}

// NewFakeOS creates a FakeOS.
func NewFakeOS() *FakeOS {
	return &FakeOS{
		errors: make(map[string]error),
	}
}

// MkdirAll is a fake call that invokes MkdirAllFn or just returns nil.
func (f *FakeOS) MkdirAll(path string, perm os.FileMode) error {
	if err := f.getError("MkdirAll"); err != nil {
		return err
	}

	if f.MkdirAllFn != nil {
		return f.MkdirAllFn(path, perm)
	}
	return nil
}

// RemoveAll is a fake call that invokes RemoveAllFn or just returns nil.
func (f *FakeOS) RemoveAll(path string) error {
	if err := f.getError("RemoveAll"); err != nil {
		return err
	}

	if f.RemoveAllFn != nil {
		return f.RemoveAllFn(path)
	}
	return nil
}

// OpenFifo is a fake call that invokes OpenFifoFn or just returns nil.
func (f *FakeOS) OpenFifo(ctx context.Context, fn string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if err := f.getError("OpenFifo"); err != nil {
		return nil, err
	}

	if f.OpenFifoFn != nil {
		return f.OpenFifoFn(ctx, fn, flag, perm)
	}
	return nil, nil
}

// Stat is a fake call that invokes StatFn or just return nil.
func (f *FakeOS) Stat(name string) (os.FileInfo, error) {
	if err := f.getError("Stat"); err != nil {
		return nil, err
	}

	if f.StatFn != nil {
		return f.StatFn(name)
	}
	return nil, nil
}

// CopyFile is a fake call that invokes CopyFileFn or just return nil.
func (f *FakeOS) CopyFile(src, dest string, perm os.FileMode) error {
	if err := f.getError("CopyFile"); err != nil {
		return err
	}

	if f.CopyFileFn != nil {
		return f.CopyFileFn(src, dest, perm)
	}
	return nil
}
