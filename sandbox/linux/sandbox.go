//go:build linux
// +build linux

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

package linux

import (
	"context"

	v2 "github.com/containerd/containerd/runtime/v2"
	"github.com/containerd/containerd/runtime/v2/runc"
)

type runcSandbox struct {
	bundle         *v2.Bundle
	pauseContainer *runc.Container
	containers     []*runc.Container
}

func (s *runcSandbox) Pause(ctx context.Context) error {
	for _, c := range s.containers {
		if err := c.Pause(ctx); err != nil {
			return err
		}
	}
	if err := s.pauseContainer.Pause(ctx); err != nil {
		return err
	}
	return nil
}

func (s *runcSandbox) Resume(ctx context.Context) error {
	for _, c := range s.containers {
		if err := c.Resume(ctx); err != nil {
			return err
		}
	}
	if err := s.pauseContainer.Resume(ctx); err != nil {
		return err
	}
	return nil
}

func (s *runcSandbox) Wait() error {
	p, err := s.pauseContainer.Process("")
	if err != nil {
		return err
	}
	p.Wait()
	return nil
}
