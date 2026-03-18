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

package apply

import (
	"errors"
	"testing"

	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestNewFileSystemApplier(t *testing.T) {
	applier := NewFileSystemApplier(nil)
	if applier == nil {
		t.Fatalf("Failed to create filesystem applier")
	}
}

func TestUnsupportedParallelApply(t *testing.T) {
	applier := NewFileSystemApplier(nil)
	if applier == nil {
		t.Fatalf("Failed to create filesystem applier")
	}
	_, err := applier.Apply(t.Context(), ocispec.Descriptor{}, nil, diff.WithParallel(true))
	if err == nil {
		t.Fatalf("An error is expected when parallel apply is not supported")
	}
	if !errors.Is(err, errdefs.ErrNotImplemented) {
		t.Errorf("Expected ErrNotImplemented error, got: %v", err)
	}
}
