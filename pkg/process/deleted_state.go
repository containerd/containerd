//go:build !windows
// +build !windows

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

package process

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/console"
	"github.com/containerd/containerd/errdefs"

	google_protobuf "github.com/gogo/protobuf/types"
)

type deletedState struct {
}

func (s *deletedState) Pause(ctx context.Context) error {
	return errors.New("cannot pause a deleted process")
}

func (s *deletedState) Resume(ctx context.Context) error {
	return errors.New("cannot resume a deleted process")
}

func (s *deletedState) Update(context context.Context, r *google_protobuf.Any) error {
	return errors.New("cannot update a deleted process")
}

func (s *deletedState) Checkpoint(ctx context.Context, r *CheckpointConfig) error {
	return errors.New("cannot checkpoint a deleted process")
}

func (s *deletedState) Resize(ws console.WinSize) error {
	return errors.New("cannot resize a deleted process")
}

func (s *deletedState) Start(ctx context.Context) error {
	return errors.New("cannot start a deleted process")
}

func (s *deletedState) Delete(ctx context.Context) error {
	return fmt.Errorf("cannot delete a deleted process: %w", errdefs.ErrNotFound)
}

func (s *deletedState) Kill(ctx context.Context, sig uint32, all bool) error {
	return fmt.Errorf("cannot kill a deleted process: %w", errdefs.ErrNotFound)
}

func (s *deletedState) SetExited(status int) {
	// no op
}

func (s *deletedState) Exec(ctx context.Context, path string, r *ExecConfig) (Process, error) {
	return nil, errors.New("cannot exec in a deleted state")
}

func (s *deletedState) Status(ctx context.Context) (string, error) {
	return "stopped", nil
}
