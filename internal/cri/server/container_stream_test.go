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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
)

// fakeStreamServer is a mock grpc.ServerStreamingServer that captures sent messages.
type fakeStreamServer[Res any] struct {
	ctx      context.Context
	sent     []*Res
	sendErr  error
	sendHook func(*Res) // optional hook called on each Send
}

func newFakeStreamServer[Res any](ctx context.Context) *fakeStreamServer[Res] {
	return &fakeStreamServer[Res]{ctx: ctx}
}

func (f *fakeStreamServer[Res]) Send(msg *Res) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.sendHook != nil {
		f.sendHook(msg)
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeStreamServer[Res]) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStreamServer[Res]) SendHeader(metadata.MD) error { return nil }
func (f *fakeStreamServer[Res]) SetTrailer(metadata.MD)       {}
func (f *fakeStreamServer[Res]) Context() context.Context     { return f.ctx }
func (f *fakeStreamServer[Res]) SendMsg(any) error            { return nil }
func (f *fakeStreamServer[Res]) RecvMsg(any) error            { return nil }

func TestStreamContainers(t *testing.T) {
	c := newTestCRIService()
	sandboxesInStore := []sandboxstore.Sandbox{
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:     "s-1abcdef1234",
				Name:   "sandboxname-1",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "podname-1"}},
			},
			sandboxstore.Status{
				State: sandboxstore.StateReady,
			},
		),
		sandboxstore.NewSandbox(
			sandboxstore.Metadata{
				ID:     "s-2abcdef1234",
				Name:   "sandboxname-2",
				Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "podname-2"}},
			},
			sandboxstore.Status{
				State: sandboxstore.StateNotReady,
			},
		),
	}
	createdAt := time.Now().UnixNano()
	startedAt := time.Now().UnixNano()
	finishedAt := time.Now().UnixNano()
	containersInStore := []containerForTest{
		{
			metadata: containerstore.Metadata{
				ID:        "c-1container",
				Name:      "name-1",
				SandboxID: "s-1abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-1"}},
			},
			status: containerstore.Status{CreatedAt: createdAt},
		},
		{
			metadata: containerstore.Metadata{
				ID:        "c-2container",
				Name:      "name-2",
				SandboxID: "s-1abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-2"}},
			},
			status: containerstore.Status{
				CreatedAt: createdAt,
				StartedAt: startedAt,
			},
		},
		{
			metadata: containerstore.Metadata{
				ID:        "c-3container",
				Name:      "name-3",
				SandboxID: "s-1abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-3"}},
			},
			status: containerstore.Status{
				CreatedAt:  createdAt,
				StartedAt:  startedAt,
				FinishedAt: finishedAt,
			},
		},
		{
			metadata: containerstore.Metadata{
				ID:        "c-4container",
				Name:      "name-4",
				SandboxID: "s-2abcdef1234",
				Config:    &runtime.ContainerConfig{Metadata: &runtime.ContainerMetadata{Name: "name-4"}},
			},
			status: containerstore.Status{
				CreatedAt: createdAt,
			},
		},
	}

	expectedContainers := []*runtime.Container{
		{
			Id:           "c-1container",
			PodSandboxId: "s-1abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-1"},
			State:        runtime.ContainerState_CONTAINER_CREATED,
			CreatedAt:    createdAt,
		},
		{
			Id:           "c-2container",
			PodSandboxId: "s-1abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-2"},
			State:        runtime.ContainerState_CONTAINER_RUNNING,
			CreatedAt:    createdAt,
		},
		{
			Id:           "c-3container",
			PodSandboxId: "s-1abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-3"},
			State:        runtime.ContainerState_CONTAINER_EXITED,
			CreatedAt:    createdAt,
		},
		{
			Id:           "c-4container",
			PodSandboxId: "s-2abcdef1234",
			Metadata:     &runtime.ContainerMetadata{Name: "name-4"},
			State:        runtime.ContainerState_CONTAINER_CREATED,
			CreatedAt:    createdAt,
		},
	}

	// Inject test sandbox metadata
	for _, sb := range sandboxesInStore {
		assert.NoError(t, c.sandboxStore.Add(sb))
	}

	// Inject test container metadata
	for _, cntr := range containersInStore {
		container, err := cntr.toContainer()
		assert.NoError(t, err)
		assert.NoError(t, c.containerStore.Add(container))
	}

	for _, testdata := range []struct {
		desc   string
		filter *runtime.ContainerFilter
		expect []*runtime.Container
	}{
		{
			desc:   "stream without filter",
			filter: &runtime.ContainerFilter{},
			expect: expectedContainers,
		},
		{
			desc: "stream filter by sandboxid",
			filter: &runtime.ContainerFilter{
				PodSandboxId: "s-1abcdef1234",
			},
			expect: expectedContainers[:3],
		},
		{
			desc: "stream filter by truncated sandboxid",
			filter: &runtime.ContainerFilter{
				PodSandboxId: "s-1",
			},
			expect: expectedContainers[:3],
		},
		{
			desc: "stream filter by containerid",
			filter: &runtime.ContainerFilter{
				Id: "c-1container",
			},
			expect: expectedContainers[:1],
		},
		{
			desc: "stream filter by truncated containerid",
			filter: &runtime.ContainerFilter{
				Id: "c-1",
			},
			expect: expectedContainers[:1],
		},
	} {
		t.Run(testdata.desc, func(t *testing.T) {
			stream := newFakeStreamServer[runtime.StreamContainersResponse](context.Background())
			err := c.StreamContainers(&runtime.StreamContainersRequest{Filter: testdata.filter}, stream)
			assert.NoError(t, err)
			require.NotEmpty(t, stream.sent)

			var containers []*runtime.Container
			for _, resp := range stream.sent {
				containers = append(containers, resp.Containers...)
			}
			assert.Len(t, containers, len(testdata.expect))
			for _, cntr := range testdata.expect {
				assert.Contains(t, containers, cntr)
			}
		})
	}
}

func TestStreamContainersEmpty(t *testing.T) {
	c := newTestCRIService()

	stream := newFakeStreamServer[runtime.StreamContainersResponse](context.Background())
	err := c.StreamContainers(&runtime.StreamContainersRequest{}, stream)
	assert.NoError(t, err)
	assert.Empty(t, stream.sent, "no messages should be sent for empty store")
}
