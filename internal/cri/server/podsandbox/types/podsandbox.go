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
	"sync"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

type PodSandbox struct {
	mu         sync.Mutex
	ID         string
	Container  containerd.Container
	State      sandboxstore.State
	Metadata   sandboxstore.Metadata
	Runtime    sandbox.RuntimeOpts
	Pid        uint32
	CreatedAt  time.Time
	stopChan   *store.StopCh
	exitStatus *containerd.ExitStatus
}

func NewPodSandbox(id string, status sandboxstore.Status) *PodSandbox {
	podSandbox := &PodSandbox{
		ID:        id,
		Container: nil,
		stopChan:  store.NewStopCh(),
		CreatedAt: status.CreatedAt,
		State:     status.State,
		Pid:       status.Pid,
	}
	if status.State == sandboxstore.StateNotReady {
		podSandbox.Exit(*containerd.NewExitStatus(status.ExitStatus, status.ExitedAt, nil))
	}
	return podSandbox
}

func (p *PodSandbox) Exit(status containerd.ExitStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exitStatus = &status
	p.State = sandboxstore.StateNotReady
	p.stopChan.Stop()
}

func (p *PodSandbox) Wait(ctx context.Context) (*containerd.ExitStatus, error) {
	s := p.GetExitStatus()
	if s != nil {
		return s, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.stopChan.Stopped():
		return p.GetExitStatus(), nil
	}
}

func (p *PodSandbox) GetExitStatus() *containerd.ExitStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitStatus
}
