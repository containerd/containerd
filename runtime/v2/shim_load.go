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
	"github.com/containerd/containerd/v2/pkg/timeout"
)

func loadShimTask(ctx context.Context, bundle *Bundle, onClose func()) (_ *shimTask, retErr error) {
	shim, err := loadShim(ctx, bundle, onClose)
	if err != nil {
		return nil, err
	}
	// Check connectivity, TaskService is the only required service, so create a temp one to check connection.
	s, err := newShimTask(shim)
	if err != nil {
		return nil, err
	}

	ctx, cancel := timeout.WithContext(ctx, loadTimeout)
	defer cancel()

	if _, err := s.PID(ctx); err != nil {
		return nil, err
	}
	return s, nil
}
