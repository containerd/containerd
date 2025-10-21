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

package podsandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/ttrpc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestGetCgroupsPath(t *testing.T) {
	testID := "test-id"
	for _, test := range []struct {
		desc          string
		cgroupsParent string
		expected      string
	}{
		{
			desc:          "should support regular cgroup path",
			cgroupsParent: "/a/b",
			expected:      "/a/b/test-id",
		},
		{
			desc:          "should support systemd cgroup path",
			cgroupsParent: "/a.slice/b.slice",
			expected:      "b.slice:cri-containerd:test-id",
		},
		{
			desc:          "should support tailing slash for regular cgroup path",
			cgroupsParent: "/a/b/",
			expected:      "/a/b/test-id",
		},
		{
			desc:          "should support tailing slash for systemd cgroup path",
			cgroupsParent: "/a.slice/b.slice/",
			expected:      "b.slice:cri-containerd:test-id",
		},
		{
			desc:          "should treat root cgroup as regular cgroup path",
			cgroupsParent: "/",
			expected:      "/test-id",
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			got := getCgroupsPath(test.cgroupsParent, testID)
			assert.Equal(t, test.expected, got)
		})
	}
}

func TestEnsureRemoveAllWithMount(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping test that requires root")
	}

	var err error
	dir1 := t.TempDir()
	dir2 := t.TempDir()

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

func TestIsShimTTRPCClosed(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ttrpc closed error text",
			err:      fmt.Errorf("ttrpc: closed"),
			expected: true,
		},
		{
			name:     "ttrpc closed error",
			err:      ttrpc.ErrClosed,
			expected: true,
		},
		{
			name:     "wrapped ttrpc closed error",
			err:      fmt.Errorf("some context: %w", ttrpc.ErrClosed),
			expected: true,
		},
		{
			name:     "non-ttrpc error",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, isShimTTRPCClosed(errgrpc.ToNative(tt.err)))
		})
	}
}
