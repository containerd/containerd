//go:build !linux

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

package seekable

import (
	"context"
	"fmt"
	"io"

	"github.com/containerd/errdefs"
)

// DMVerity is linux-only; this is a placeholder so builds work outside of linux.
type DMVerityMetadata struct {
	RootDigest string `json:"roothash"`
	HashOffset uint64 `json:"hashoffset"`
}

func IsValidDMVerityBlockSize(_ int) bool {
	return false
}

// WriteDMVeritySkippableFrameAtEOF is not supported on non-Linux platforms.
func WriteDMVeritySkippableFrameAtEOF(_ context.Context, _ *countingWriter, _ string, _ int64, _ int) (int64, string, int64, error) {
	return 0, "", 0, fmt.Errorf("dm-verity encoding is not supported on this platform: %w", errdefs.ErrNotImplemented)
}

// MaterializeDMVerity is not supported on non-Linux platforms.
func MaterializeDMVerity(_ context.Context, _ io.ReaderAt, _ int64, _ int64, _ string, _ string) (DMVerityMetadata, error) {
	return DMVerityMetadata{}, fmt.Errorf("dm-verity materialization is not supported on this platform: %w", errdefs.ErrNotImplemented)
}

// ReadDMVerityMetadata is not supported on non-Linux platforms.
func ReadDMVerityMetadata(_ string) (DMVerityMetadata, error) {
	return DMVerityMetadata{}, fmt.Errorf("dm-verity is not supported on this platform: %w", errdefs.ErrNotImplemented)
}
