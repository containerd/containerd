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

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	"golang.org/x/sys/unix"
)

func TestGetCgroupsPath(t *testing.T) {
	testID := "test-id"
	for desc, test := range map[string]struct {
		cgroupsParent string
		expected      string
	}{
		"should support regular cgroup path": {
			cgroupsParent: "/a/b",
			expected:      "/a/b/test-id",
		},
		"should support systemd cgroup path": {
			cgroupsParent: "/a.slice/b.slice",
			expected:      "b.slice:cri-containerd:test-id",
		},
		"should support tailing slash for regular cgroup path": {
			cgroupsParent: "/a/b/",
			expected:      "/a/b/test-id",
		},
		"should support tailing slash for systemd cgroup path": {
			cgroupsParent: "/a.slice/b.slice/",
			expected:      "b.slice:cri-containerd:test-id",
		},
		"should treat root cgroup as regular cgroup path": {
			cgroupsParent: "/",
			expected:      "/test-id",
		},
	} {
		t.Logf("TestCase %q", desc)
		got := getCgroupsPath(test.cgroupsParent, testID)
		assert.Equal(t, test.expected, got)
	}
}

func TestEnsureRemoveAllWithMount(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping test that requires root")
	}

	dir1, err := os.MkdirTemp("", "test-ensure-removeall-with-dir1")
	if err != nil {
		t.Fatal(err)
	}
	dir2, err := os.MkdirTemp("", "test-ensure-removeall-with-dir2")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir2)

	bindDir := filepath.Join(dir1, "bind")
	if err := os.MkdirAll(bindDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := unix.Mount(dir2, bindDir, "none", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		err = ensureRemoveAll(context.Background(), dir1)
		close(done)
	}()

	select {
	case <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for EnsureRemoveAll to finish")
	}

	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Fatalf("expected %q to not exist", dir1)
	}
}
