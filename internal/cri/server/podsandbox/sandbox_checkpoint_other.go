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

package podsandbox

import (
	"context"

	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/errdefs"
)

func (c *CheckpointService) Checkpoint(context.Context, string, sandbox.CheckpointOptions) error {
	return errdefs.ErrNotImplemented
}

func (c *CheckpointService) Restore(context.Context, string, sandbox.RestoreOptions) (sandbox.RestoreResult, error) {
	return sandbox.RestoreResult{}, errdefs.ErrNotImplemented
}

func (c *CheckpointService) Recover(context.Context) error {
	return nil
}
