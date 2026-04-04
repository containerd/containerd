//go:build linux

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
	"fmt"
	"testing"
	"time"

	"github.com/containerd/containerd/api/services/tasks/v1"
	criconfig "github.com/containerd/containerd/v2/internal/cri/config"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/internal/cri/store/stats"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

type mockTasksClient struct {
	tasks.TasksClient
	metricsFunc func(ctx context.Context, in *tasks.MetricsRequest) (*tasks.MetricsResponse, error)
}

func (m *mockTasksClient) Metrics(ctx context.Context, in *tasks.MetricsRequest, opts ...grpc.CallOption) (*tasks.MetricsResponse, error) {
	return m.metricsFunc(ctx, in)
}

func TestCollectContainerStats(t *testing.T) {
	root := t.TempDir()
	c := NewStatsCollector(criconfig.Config{})
	metricsCalled := false
	mockTasks := &mockTasksClient{
		metricsFunc: func(ctx context.Context, in *tasks.MetricsRequest) (*tasks.MetricsResponse, error) {
			metricsCalled = true
			assert.Len(t, in.Filters, 1)
			assert.Equal(t, "id==c1", in.Filters[0])
			return &tasks.MetricsResponse{}, nil
		},
	}

	// Create a running container
	c1, err := containerstore.NewContainer(
		containerstore.Metadata{ID: "c1", SandboxID: "s1"},
		containerstore.WithStatus(containerstore.Status{
			StartedAt: time.Now().UnixNano(),
		}, root),
	)
	assert.NoError(t, err)

	// Create an exited container
	c2, err := containerstore.NewContainer(
		containerstore.Metadata{ID: "c2", SandboxID: "s1"},
		containerstore.WithStatus(containerstore.Status{
			StartedAt:  time.Now().UnixNano(),
			FinishedAt: time.Now().UnixNano(),
		}, root),
	)
	assert.NoError(t, err)

	c.SetDependencies(mockTasks, func() []containerstore.Container {
		return []containerstore.Container{c1, c2}
	}, func() []sandboxstore.Sandbox {
		return nil
	})

	// Add containers to collector stores
	c.AddContainer("c1")
	c.AddContainer("c2")

	c.collectContainerStats(context.Background())

	assert.True(t, metricsCalled)

	// Verify that c2 was removed from stores
	c.mu.RLock()
	_, ok1 := c.stores["c1"]
	_, ok2 := c.stores["c2"]
	c.mu.RUnlock()
	assert.True(t, ok1)
	assert.False(t, ok2)
}

func TestCollectSandboxStats(t *testing.T) {
	c := NewStatsCollector(criconfig.Config{})

	// Create a ready sandbox
	sb1 := sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{ID: "sb1"},
		Status:   sandboxstore.StoreStatus(sandboxstore.Status{State: sandboxstore.StateReady}),
	}

	// Create a not ready sandbox
	sb2 := sandboxstore.Sandbox{
		Metadata: sandboxstore.Metadata{ID: "sb2"},
		Status:   sandboxstore.StoreStatus(sandboxstore.Status{State: sandboxstore.StateNotReady}),
	}

	c.SetDependencies(nil, func() []containerstore.Container {
		return nil
	}, func() []sandboxstore.Sandbox {
		return []sandboxstore.Sandbox{sb1, sb2}
	})

	// Add sandboxes to collector stores
	c.AddContainer("sb1")
	c.AddContainer("sb2")

	c.collectSandboxStats(context.Background())

	c.mu.RLock()
	_, ok1 := c.stores["sb1"]
	_, ok2 := c.stores["sb2"]
	c.mu.RUnlock()
	assert.True(t, ok1)
	assert.False(t, ok2)
}

func BenchmarkCollectContainerStats(b *testing.B) {
	root := b.TempDir()
	c := NewStatsCollector(criconfig.Config{})
	mockTasks := &mockTasksClient{
		metricsFunc: func(ctx context.Context, in *tasks.MetricsRequest) (*tasks.MetricsResponse, error) {
			return &tasks.MetricsResponse{}, nil
		},
	}

	const totalContainers = 1000

	runBench := func(b *testing.B, runningCount int) {
		var containers []containerstore.Container
		c.mu.Lock()
		c.stores = make(map[string]*stats.TimedStore)
		c.mu.Unlock()

		for i := 0; i < totalContainers; i++ {
			id := fmt.Sprintf("c-%d-%d", runningCount, i)
			status := containerstore.Status{StartedAt: time.Now().UnixNano()}
			if i >= runningCount {
				status.FinishedAt = time.Now().UnixNano()
			}
			cntr, _ := containerstore.NewContainer(
				containerstore.Metadata{ID: id},
				containerstore.WithStatus(status, root),
			)
			containers = append(containers, cntr)
			c.AddContainer(id)
		}

		c.SetDependencies(mockTasks, func() []containerstore.Container {
			return containers
		}, func() []sandboxstore.Sandbox {
			return nil
		})

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.collectContainerStats(context.Background())
		}
	}

	b.Run("AllRunning", func(b *testing.B) {
		runBench(b, totalContainers)
	})

	b.Run("MostlyStopped", func(b *testing.B) {
		runBench(b, 10)
	})
}
