//go:build linux

/*
   Copyright The containerd Authors.

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

package process

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func TestNewBinaryIO(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	uri, _ := url.Parse("binary:///bin/echo?test")

	before := descriptorCount(t)

	io, err := NewBinaryIO(ctx, "1", uri)
	if err != nil {
		t.Fatal(err)
	}

	err = io.Close()
	if err != nil {
		t.Fatal(err)
	}

	after := descriptorCount(t)
	if before != after-1 { // one descriptor must be closed from shim logger side
		t.Fatalf("some descriptors weren't closed (%d != %d -1)", before, after)
	}
}

func TestNewBinaryIOCleanup(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	uri, _ := url.Parse("binary:///not/existing")

	before := descriptorCount(t)
	_, err := NewBinaryIO(ctx, "2", uri)
	if err == nil {
		t.Fatal("error expected for invalid binary")
	}

	after := descriptorCount(t)
	if before != after {
		t.Fatalf("some descriptors weren't closed (%d != %d)", before, after)
	}
}

func descriptorCount(t *testing.T) int {
	t.Helper()
	files, _ := os.ReadDir("/proc/self/fd")

	// Go 1.23 introduced a new internal file descriptor type "pidfd"
	// that we don't want to count towards the total file descriptors in
	// use by the process. This retains the behavior of previous Go
	// versions.
	// See https://go.dev/issues/62654.
	//
	// Once the proposal to check for internal file descriptors is
	// accepted, we can use that instead to detect internal fds in use
	// by the Go runtime.
	// See https://go.dev/issues/67639.
	for i, file := range files {
		sym, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%s", file.Name()))
		if err != nil {
			// ignore fds that cannot be followed.
			continue
		}

		if strings.HasPrefix(sym, "pidfd:") {
			files = append(files[:i], files[i+1:]...)
		}
	}

	return len(files)
}
