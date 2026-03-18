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

package v2

import (
	"context"
	"io"
	"os"

	"github.com/containerd/log"
)

func copyShimlog(ctx context.Context, _ string, f io.ReadCloser, _ chan struct{}) error {
	defer f.Close()
	_, err := io.Copy(os.Stderr, f)
	// To prevent flood of error messages, the expected error
	// should be reset, like os.ErrClosed or os.ErrNotExist, which
	// depends on platform.
	err = checkCopyShimLogError(ctx, err)
	if err != nil {
		log.G(ctx).WithError(err).Error("copy shim log")
	}
	return nil
}
