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
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
)

func TestHostDirFromRootsPrefersFirstMatchingRoot(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root1, "_default"), 0o755); err != nil {
		t.Fatalf("failed to create first root host dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root2, "_default"), 0o755); err != nil {
		t.Fatalf("failed to create second root host dir: %v", err)
	}

	hostFn := hostDirFromRoots([]string{root1, root2})
	dir, err := hostFn("docker.io")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := filepath.Join(root1, "_default")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestHostDirFromRootsFallsBackToNextRoot(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root2, "_default"), 0o755); err != nil {
		t.Fatalf("failed to create second root host dir: %v", err)
	}

	hostFn := hostDirFromRoots([]string{root1, root2})
	dir, err := hostFn("docker.io")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	want := filepath.Join(root2, "_default")
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestHostDirFromRootsNotFound(t *testing.T) {
	hostFn := hostDirFromRoots([]string{t.TempDir(), t.TempDir()})
	_, err := hostFn("docker.io")
	if !errdefs.IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}
