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

package tasks

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/errdefs"

	"github.com/containerd/containerd/v2/core/runtime"
	"github.com/containerd/containerd/v2/pkg/filters"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
)

type statsTask struct {
	runtime.Task
	id    string
	stats func() (*types.Any, error)
}

func (t *statsTask) ID() string        { return t.id }
func (t *statsTask) Namespace() string { return "default" }
func (t *statsTask) Stats(context.Context) (*types.Any, error) {
	return t.stats()
}

func TestGetTasksMetricsConcurrent(t *testing.T) {
	if goruntime.GOMAXPROCS(0) < 2 {
		t.Skip("needs GOMAXPROCS >= 2")
	}
	var inflight atomic.Int32
	release := make(chan struct{})
	overlap := func() (*types.Any, error) {
		if inflight.Add(1) == 2 {
			close(release)
		}
		select {
		case <-release:
			return &types.Any{}, nil
		case <-time.After(5 * time.Second):
			return nil, errors.New("Stats calls did not overlap")
		}
	}
	tasks := []runtime.Task{
		&statsTask{id: "a", stats: overlap},
		&statsTask{id: "b", stats: overlap},
	}
	var resp api.MetricsResponse
	getTasksMetrics(context.Background(), filters.Always, tasks, &resp)
	if len(resp.Metrics) != 2 {
		t.Fatalf("got %d metrics, want 2", len(resp.Metrics))
	}
}

func TestGetTasksMetricsOrderAndErrors(t *testing.T) {
	var tasks []runtime.Task
	for i := range 20 {
		tasks = append(tasks, &statsTask{id: fmt.Sprintf("t%02d", i), stats: func() (*types.Any, error) {
			switch i % 3 {
			case 1:
				return nil, errdefs.ErrNotFound
			case 2:
				return nil, errors.New("boom")
			}
			return &types.Any{}, nil
		}})
	}
	var resp api.MetricsResponse
	getTasksMetrics(context.Background(), filters.Always, tasks, &resp)
	var got []string
	for _, m := range resp.Metrics {
		got = append(got, m.ID)
	}
	want := []string{"t00", "t03", "t06", "t09", "t12", "t15", "t18"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
