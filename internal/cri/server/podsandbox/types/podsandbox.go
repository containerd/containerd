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

package types

import (
	"context"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

type PodSandbox struct {
	ID        string
	Container containerd.Container
	Metadata  sandboxstore.Metadata
	Runtime   sandbox.RuntimeOpts
	Status    sandboxstore.StatusStorage
	stopChan  *store.StopCh
}

func NewPodSandbox(id string, status sandboxstore.Status) *PodSandbox {
	podSandbox := &PodSandbox{
		ID:        id,
		Container: nil,
		stopChan:  store.NewStopCh(),
		Status:    sandboxstore.StoreStatus(status),
	}
	if status.State == sandboxstore.StateNotReady {
		podSandbox.stopChan.Stop()
	}
	return podSandbox
}

func (p *PodSandbox) Exit(code uint32, exitTime time.Time) error {
	if err := p.Status.Update(func(status sandboxstore.Status) (sandboxstore.Status, error) {
		status.State = sandboxstore.StateNotReady
		status.ExitStatus = code
		status.ExitedAt = exitTime
		status.Pid = 0
		return status, nil
	}); err != nil {
		return err
	}
	p.stopChan.Stop()
	return nil
}

func (p *PodSandbox) Wait(ctx context.Context) (containerd.ExitStatus, error) {
	select {
	case <-ctx.Done():
		return containerd.ExitStatus{}, ctx.Err()
	case <-p.stopChan.Stopped():
		status := p.Status.Get()
		return *containerd.NewExitStatus(status.ExitStatus, status.ExitedAt, nil), nil
	}
}
