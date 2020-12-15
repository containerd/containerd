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

package oci

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/pkg/testutil"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func TestAddCaps(t *testing.T) {
	t.Parallel()

	var s specs.Spec

	if err := WithAddedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if !capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d does not contain added cap", i)
		}
	}
}

func TestDropCaps(t *testing.T) {
	t.Parallel()

	var s specs.Spec

	if err := WithAllKnownCapabilities(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}

	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d contains dropped cap", i)
		}
	}

	// Add all capabilities back and drop a different cap.
	if err := WithAllKnownCapabilities(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_FOWNER"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}

	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if capsContain(cl, "CAP_FOWNER") {
			t.Errorf("cap list %d contains dropped cap", i)
		}
		if !capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d doesn't contain non-dropped cap", i)
		}
	}

	// Drop all duplicated caps.
	if err := WithCapabilities([]string{"CAP_CHOWN", "CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if len(cl) != 0 {
			t.Errorf("cap list %d is not empty", i)
		}
	}
}

func TestGetDevices(t *testing.T) {
	testutil.RequiresRoot(t)

	dir, err := ioutil.TempDir("/dev", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	zero := filepath.Join(dir, "zero")
	if err := ioutil.WriteFile(zero, nil, 0600); err != nil {
		t.Fatal(err)
	}

	if err := unix.Mount("/dev/zero", zero, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	defer unix.Unmount(filepath.Join(dir, "zero"), unix.MNT_DETACH)

	t.Run("single device", func(t *testing.T) {
		t.Run("no container path", func(t *testing.T) {
			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}

			if len(devices) != 1 {
				t.Fatalf("expected one device %v", devices)
			}
			if devices[0].Path != zero {
				t.Fatalf("got unexpected device path %s", devices[0].Path)
			}
		})
		t.Run("with container path", func(t *testing.T) {
			newPath := "/dev/testNew"
			devices, err := getDevices(dir, newPath)
			if err != nil {
				t.Fatal(err)
			}

			if len(devices) != 1 {
				t.Fatalf("expected one device %v", devices)
			}
			if devices[0].Path != filepath.Join(newPath, "zero") {
				t.Fatalf("got unexpected device path %s", devices[0].Path)
			}
		})
	})
	t.Run("With symlink in dir", func(t *testing.T) {
		if err := os.Symlink("/dev/zero", filepath.Join(dir, "zerosym")); err != nil {
			t.Fatal(err)
		}
		devices, err := getDevices(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != 1 {
			t.Fatalf("expected one device %v", devices)
		}
		if devices[0].Path != filepath.Join(dir, "zero") {
			t.Fatalf("got unexpected device path, expected %q, got %q", filepath.Join(dir, "zero"), devices[0].Path)
		}
	})
	t.Run("No devices", func(t *testing.T) {
		dir := dir + "2"
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)

		t.Run("empty dir", func(T *testing.T) {
			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(devices) != 0 {
				t.Fatalf("expected no devices, got %+v", devices)
			}
		})
		t.Run("symlink to device in dir", func(t *testing.T) {
			if err := os.Symlink("/dev/zero", filepath.Join(dir, "zerosym")); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(dir, "zerosym"))

			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(devices) != 0 {
				t.Fatalf("expected no devices, got %+v", devices)
			}
		})
		t.Run("regular file in dir", func(t *testing.T) {
			if err := ioutil.WriteFile(filepath.Join(dir, "somefile"), []byte("hello"), 0600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(dir, "somefile"))

			devices, err := getDevices(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(devices) != 0 {
				t.Fatalf("expected no devices, got %+v", devices)
			}
		})
	})
}
