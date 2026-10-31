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

package erofsutils_test

// Linux-specific benchmarks that exercise the xattr second-pass in
// ConvertErofs. These use the "user." xattr namespace which requires no root
// privileges on most filesystems.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/containerd/containerd/v2/internal/erofsutils"
	"github.com/stretchr/testify/require"
)

// makeSourceDirWithXattrs builds a directory tree of approximately payloadBytes
// bytes and sets one "user.meta" xattr on every file and directory. This
// exercises the syscall path in applyXattrs.
func makeSourceDirWithXattrs(b *testing.B, payloadBytes int) string {
	b.Helper()
	dir := b.TempDir()
	fileData := bytes.Repeat([]byte("deadbeef"), 512)
	count := payloadBytes / len(fileData)
	if count == 0 {
		count = 1
	}
	setxattr := func(path string) {
		b.Helper()
		if err := unix.Lsetxattr(path, "user.meta", []byte("bench"), 0); err != nil {
			b.Skipf("filesystem does not support user xattrs: %v", err)
		}
	}
	for i := 0; i < count; i++ {
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", i))
		require.NoError(b, os.MkdirAll(sub, 0755))
		setxattr(sub)
		fpath := filepath.Join(sub, "file")
		require.NoError(b, os.WriteFile(fpath, fileData, 0644))
		setxattr(fpath)
	}
	return dir
}

// BenchmarkConvertDirErofs_Xattr_1MB measures ConvertErofs on a 1 MiB
// directory tree where every entry carries one user.meta xattr. This ensures
// the applyXattrs second-pass does not introduce disproportionate overhead.
func BenchmarkConvertDirErofs_Xattr_1MB(b *testing.B) {
	benchDirGoXattr(b, 1<<20)
}

// BenchmarkConvertDirErofs_Xattr_16MB is the 16 MiB variant.
func BenchmarkConvertDirErofs_Xattr_16MB(b *testing.B) {
	benchDirGoXattr(b, 16<<20)
}

func benchDirGoXattr(b *testing.B, size int) {
	src := makeSourceDirWithXattrs(b, size)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(b.TempDir(), "layer.erofs")
		if err := erofsutils.ConvertErofs(ctx, out, src); err != nil {
			b.Fatal(err)
		}
	}
}
