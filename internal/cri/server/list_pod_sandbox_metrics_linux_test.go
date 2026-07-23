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
	"errors"
	"testing"

	cg2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// fakeMetricsContainer implements just enough of containerd.Container for collectContainerMetrics.
// Non-overridden calls to embedded-interface methods panic.
type fakeMetricsContainer struct {
	containerd.Container
	task    containerd.Task
	taskErr error
}

func (c *fakeMetricsContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	if c.taskErr != nil {
		return nil, c.taskErr
	}
	return c.task, nil
}

func (c *fakeMetricsContainer) Info(context.Context, ...containerd.InfoOpts) (containers.Container, error) {
	return containers.Container{}, errors.New("not implemented")
}

type fakeMetricsTask struct {
	containerd.Task
	metrics *types.Metric
}

func (t *fakeMetricsTask) Metrics(context.Context) (*types.Metric, error) {
	return t.metrics, nil
}

func (t *fakeMetricsTask) Spec(context.Context) (*oci.Spec, error) {
	return nil, errors.New("not implemented")
}

func (t *fakeMetricsTask) Pids(context.Context) ([]containerd.ProcessInfo, error) {
	return nil, errors.New("not implemented")
}

func TestListPodSandboxMetrics(t *testing.T) {
	c := newTestCRIService()

	require.NoError(t, c.sandboxStore.Add(sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID: "s1",
			Config: &runtime.PodSandboxConfig{
				Metadata: &runtime.PodSandboxMetadata{Name: "sandbox", Namespace: "ns"},
			},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)))

	metricsData, err := typeurl.MarshalAnyToProto(&cg2.Metrics{CPU: &cg2.CPUStat{UsageUsec: 100}})
	require.NoError(t, err)

	newContainer := func(id string, container containerd.Container) containerstore.Container {
		internalContainer, err := containerstore.NewContainer(
			containerstore.Metadata{
				ID:        id,
				SandboxID: "s1",
				Config: &runtime.ContainerConfig{
					Metadata: &runtime.ContainerMetadata{Name: id},
					Image:    &runtime.ImageSpec{UserSpecifiedImage: "test-image"},
				},
			},
			containerstore.WithFakeStatus(containerstore.Status{}),
			containerstore.WithContainer(container),
		)
		require.NoError(t, err)
		return internalContainer
	}

	require.NoError(t, c.containerStore.Add(newContainer("running", &fakeMetricsContainer{
		task: &fakeMetricsTask{metrics: &types.Metric{Data: metricsData}},
	})))

	resp, err := c.ListPodSandboxMetrics(t.Context(), &runtime.ListPodSandboxMetricsRequest{})
	require.NoError(t, err)

	require.Len(t, resp.PodMetrics, 1)
	podMetrics := resp.PodMetrics[0]
	assert.Equal(t, "s1", podMetrics.PodSandboxId)
	require.Len(t, podMetrics.ContainerMetrics, 1)
	assert.Equal(t, "running", podMetrics.ContainerMetrics[0].ContainerId)
	assert.NotEmpty(t, podMetrics.ContainerMetrics[0].Metrics)
}
