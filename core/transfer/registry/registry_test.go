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

package registry

import (
	"context"
	"reflect"
	"testing"
)

func TestWithHostDir_AppendsInCallOrder(t *testing.T) {
	var ropts registryOpts

	if err := WithHostDir("")(&ropts); err != nil {
		t.Fatalf("WithHostDir(empty): %v", err)
	}
	if err := WithHostDir("/etc/containerd/certs.d")(&ropts); err != nil {
		t.Fatalf("WithHostDir(first): %v", err)
	}
	if err := WithHostDir("/etc/docker/certs.d")(&ropts); err != nil {
		t.Fatalf("WithHostDir(second): %v", err)
	}

	want := []string{"/etc/containerd/certs.d", "/etc/docker/certs.d"}
	if !reflect.DeepEqual(ropts.hostDirs, want) {
		t.Fatalf("unexpected hostDirs: got %v, want %v", ropts.hostDirs, want)
	}
}

func TestNewOCIRegistry_PreservesFirstHostDirForTransferMarshaling(t *testing.T) {
	reg, err := NewOCIRegistry(
		context.Background(),
		"registry.k8s.io/pause:3.10",
		WithHostDir("/etc/containerd/certs.d"),
		WithHostDir("/etc/docker/certs.d"),
	)
	if err != nil {
		t.Fatalf("NewOCIRegistry() error = %v", err)
	}

	if reg.hostDir != "/etc/containerd/certs.d" {
		t.Fatalf("unexpected marshaling hostDir: got %q, want %q", reg.hostDir, "/etc/containerd/certs.d")
	}
}
