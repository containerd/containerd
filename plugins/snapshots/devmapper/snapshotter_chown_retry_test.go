//go:build linux

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

// TestChownRetry verifies that fstest.Chown() will retry and eventually succeed
// after transient EBUSY errors. 1000 iterations ensure the fix is stable.
func TestChownRetry(t *testing.T) {
	const loops = 1000
	for i := 0; i < loops; i++ {
		root := t.TempDir()

		// 建立要被 Chown 的檔案
		target := filepath.Join(root, "foo")
		require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

		// 置換 ChownFunc：前兩次回傳 EBUSY，之後成功
		var fails int32 = 2
		orig := fstest.ChownFunc
		fstest.ChownFunc = func(p string, uid, gid int) error {
			if atomic.AddInt32(&fails, -1) >= 0 {
				return unix.EBUSY
			}
			return nil
		}
		// 還原全域變數
		defer func() { fstest.ChownFunc = orig }()

		err := fstest.Chown("foo", 0, 0).Apply(root)
		require.NoError(t, err)
	}
}
