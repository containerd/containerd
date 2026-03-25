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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/server/podsandbox"
	sstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type fakeSandboxStore struct {
	sandbox.Store
	updatedSandbox *sandbox.Sandbox
	getErr         error
	updateErr      error
}

func (f *fakeSandboxStore) Get(_ context.Context, id string) (sandbox.Sandbox, error) {
	if f.getErr != nil {
		return sandbox.Sandbox{}, f.getErr
	}
	return sandbox.Sandbox{ID: id, Extensions: map[string]typeurl.Any{}}, nil
}

func (f *fakeSandboxStore) Update(_ context.Context, sb sandbox.Sandbox, _ ...string) (sandbox.Sandbox, error) {
	if f.updateErr != nil {
		return sandbox.Sandbox{}, f.updateErr
	}
	f.updatedSandbox = &sb
	return sb, nil
}

type recordSandboxService struct {
	fakeSandboxService
	calledID  string
	returnErr error
}

func (s *recordSandboxService) UpdateSandbox(_ context.Context, _ string, sandboxID string, _ sandbox.Sandbox, _ ...string) error {
	s.calledID = sandboxID
	return s.returnErr
}

func TestUpdatePodSandboxResources(t *testing.T) {
	mySandbox := sstore.Metadata{
		ID: "test-sandbox-id",
		Config: &runtime.PodSandboxConfig{
			Metadata: &runtime.PodSandboxMetadata{Name: "test-name"},
		},
	}

	t.Run("fails when sandbox not found in local store", func(t *testing.T) {
		c := newTestCRIService(func(*criService) {})
		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "missing-sandbox-id",
		}
		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find sandbox")
	})

	t.Run("fails when core store get fails", func(t *testing.T) {
		fakeStore := &fakeSandboxStore{getErr: errdefs.ErrNotFound}
		c := newTestCRIService(func(service *criService) {
			client, _ := containerd.New("", containerd.WithServices(containerd.WithSandboxStore(fakeStore)))
			service.client = client
		})
		s := sstore.NewSandbox(mySandbox, sstore.Status{})
		c.sandboxStore.Add(s)

		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "test-sandbox-id",
		}
		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get sandbox test-sandbox-id from sandbox store")
	})

	t.Run("fails when core store update fails", func(t *testing.T) {
		fakeStore := &fakeSandboxStore{updateErr: errdefs.ErrUnavailable}
		c := newTestCRIService(func(service *criService) {
			client, _ := containerd.New("", containerd.WithServices(containerd.WithSandboxStore(fakeStore)))
			service.client = client
		})
		s := sstore.NewSandbox(mySandbox, sstore.Status{})
		c.sandboxStore.Add(s)

		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "test-sandbox-id",
		}
		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update sandbox test-sandbox-id in core store")
	})

	t.Run("successfully extracts and delegates resources to stores", func(t *testing.T) {
		fakeStore := &fakeSandboxStore{}
		c := newTestCRIService(func(service *criService) {
			client, _ := containerd.New("", containerd.WithServices(containerd.WithSandboxStore(fakeStore)))
			service.client = client
		})
		s := sstore.NewSandbox(mySandbox, sstore.Status{})
		c.sandboxStore.Add(s)

		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "test-sandbox-id",
			Overhead: &runtime.LinuxContainerResources{
				MemoryLimitInBytes: 100,
			},
			Resources: &runtime.LinuxContainerResources{
				CpuQuota: 200,
			},
		}

		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.NoError(t, err)

		// Assert local mem store mutated correctly.
		localSb, err := c.sandboxStore.Get("test-sandbox-id")
		require.NoError(t, err)
		status := localSb.Status.Get()
		require.NotNil(t, status.Overhead)
		require.NotNil(t, status.Resources)
		assert.Equal(t, int64(100), status.Overhead.Linux.MemoryLimitInBytes)
		assert.Equal(t, int64(200), status.Resources.Linux.CpuQuota)

		// Assert core store was updated with correct extensions.
		require.NotNil(t, fakeStore.updatedSandbox)
		ext, ok := fakeStore.updatedSandbox.Extensions[podsandbox.UpdatedResourcesKey]
		require.True(t, ok, "expected UpdatedResourcesKey in extensions")

		var updatedRes podsandbox.UpdatedResources
		err = typeurl.UnmarshalTo(ext, &updatedRes)
		require.NoError(t, err)

		require.NotNil(t, updatedRes.Overhead)
		require.NotNil(t, updatedRes.Resources)
		assert.Equal(t, int64(100), updatedRes.Overhead.MemoryLimitInBytes)
		assert.Equal(t, int64(200), updatedRes.Resources.CpuQuota)
	})

	t.Run("success fallback when sandbox controller does not implement update", func(t *testing.T) {
		recordService := &recordSandboxService{returnErr: errdefs.ErrNotImplemented}
		c := newTestCRIService(func(service *criService) {
			service.sandboxService = recordService
			client, _ := containerd.New("", containerd.WithServices(containerd.WithSandboxStore(&fakeSandboxStore{})))
			service.client = client
		})
		s := sstore.NewSandbox(mySandbox, sstore.Status{})
		c.sandboxStore.Add(s)

		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "test-sandbox-id",
		}

		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.NoError(t, err)

		assert.Equal(t, "test-sandbox-id", recordService.calledID)
	})

	t.Run("success when sandbox controller implements update", func(t *testing.T) {
		recordService := &recordSandboxService{returnErr: nil}
		c := newTestCRIService(func(service *criService) {
			service.sandboxService = recordService
			client, _ := containerd.New("", containerd.WithServices(containerd.WithSandboxStore(&fakeSandboxStore{})))
			service.client = client
		})
		s := sstore.NewSandbox(mySandbox, sstore.Status{})
		c.sandboxStore.Add(s)

		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "test-sandbox-id",
		}

		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.NoError(t, err)

		assert.Equal(t, "test-sandbox-id", recordService.calledID)
	})

	t.Run("fails when sandbox controller update fails", func(t *testing.T) {
		recordService := &recordSandboxService{returnErr: errdefs.ErrInvalidArgument}
		c := newTestCRIService(func(service *criService) {
			service.sandboxService = recordService
			client, _ := containerd.New("", containerd.WithServices(containerd.WithSandboxStore(&fakeSandboxStore{})))
			service.client = client
		})
		s := sstore.NewSandbox(mySandbox, sstore.Status{})
		c.sandboxStore.Add(s)

		req := &runtime.UpdatePodSandboxResourcesRequest{
			PodSandboxId: "test-sandbox-id",
		}

		_, err := c.UpdatePodSandboxResources(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), errdefs.ErrInvalidArgument.Error())
	})
}
