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

package devmapper

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/pkg/fstest"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestChownRetryMechanism(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	t.Run("chown_retry_with_ebusy", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "test_file")

		require.NoError(t, os.WriteFile(target, []byte("test content"), 0o644))

		originalChownFunc := fstest.ChownFunc
		defer func() {
			fstest.ChownFunc = originalChownFunc
		}()

		var callCount int32
		fstest.ChownFunc = func(path string, uid, gid int) error {
			count := atomic.AddInt32(&callCount, 1)
			if count <= 2 {
				return unix.EBUSY
			}
			return nil
		}

		err := fstest.Chown("test_file", 0, 0).Apply(root)
		require.NoError(t, err, "Chown should succeed after retries")

		finalCount := atomic.LoadInt32(&callCount)
		require.Equal(t, int32(3), finalCount, "Should have made exactly 3 attempts")
	})
}
