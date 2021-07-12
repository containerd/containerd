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

package server

import (
	"github.com/containerd/containerd"

	"github.com/containerd/containerd/pkg/cri/store"
	"github.com/containerd/containerd/pkg/cri/store/sandbox"
	"github.com/containerd/containerd/pkg/netns"
)

// Sandbox contains all resources associated with the sandbox. All methods to
// mutate the internal state are thread safe.
type Sandbox struct {
	// Metadata is the metadata of the sandbox, it is immutable after created.
	sandbox.Metadata
	// Status stores the status of the sandbox.
	Status sandbox.StatusStorage
	// Container is the containerd sandbox container client.
	Container containerd.Container
	// CNI network namespace client.
	// For hostnetwork pod, this is always nil;
	// For non hostnetwork pod, this should never be nil.
	NetNS *netns.NetNS
	// StopCh is used to propagate the stop information of the sandbox.
	*store.StopCh
}

// NewSandbox creates an internally used sandbox type. This functions reminds
// the caller that a sandbox must have a status.
func NewSandbox(metadata sandbox.Metadata, status sandbox.Status) *Sandbox {
	s := &Sandbox{
		Metadata: metadata,
		Status:   sandbox.StoreStatus(status),
		StopCh:   store.NewStopCh(),
	}
	if status.State == sandbox.StateNotReady {
		s.Stop()
	}
	return s
}

// GetMetadata implement sandbox.Sandbox interface
func (s *Sandbox) GetMetadata() *sandbox.Metadata {
	return &s.Metadata
}

// GetStatus implement sandbox.Sandbox interface
func (s *Sandbox) GetStatus() sandbox.StatusStorage {
	return s.Status
}

// Stop implement sandbox.Sandbox interface
func (s *Sandbox) Stop() {
	s.StopCh.Stop()
}

// Stopped implement sandbox.Sandbox interface
func (s *Sandbox) Stopped() <-chan struct{} {
	return s.StopCh.Stopped()
}
