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

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	containerd "github.com/containerd/containerd/v2/client"
	containerstore "github.com/containerd/containerd/v2/internal/cri/store/container"
	sandboxstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/containerd/v2/pkg/cio"
)

// Characterization test for the contract: a transient per-container metric error
// must not erase the rest of the pod's metrics from ListPodSandboxMetrics. This
// test must PASS both before and after any refactor; it currently FAILS on the
// unpatched code, which is the reproduction of the dropped-pod-metrics bug.
//
// Background:
//   In ListPodSandboxMetrics (list_pod_sandbox_metrics_linux.go) each sandbox is
//   processed in its own goroutine: it first collects pod-level metrics into
//   sandboxMetrics, then loops over the sandbox's containers, and only at the end
//   appends sandboxMetrics to the response. When collectContainerMetrics returns a
//   transient error (Unavailable/NotFound) the code does `return nil` (lines 112
//   and 120) instead of `continue`, despite the comment "just log and continue".
//   `return nil` ends the whole goroutine before the final append, so the entire
//   pod -- its pod-level metrics and any already-collected sibling containers --
//   is silently dropped from the response.
//
// Faithful, recommended-usage reproduction (no running runtime, no root, no real
// network namespace):
//   - Drives the real exported ListPodSandboxMetrics via the project's own
//     newTestCRIService/NewSandbox/NewContainer test seams.
//   - The only test double is Container.Task() returning errdefs.ErrNotFound,
//     which is exactly what containerd returns when a container's task has just
//     exited between the container-store snapshot and metric collection -- the
//     normal, high-frequency churn this code path is meant to tolerate.
//   - The sandbox has no NetNSPath, so collectPodSandboxMetrics succeeds and
//     returns a non-nil PodSandboxMetrics carrying PodSandboxId (no netns needed).

// fakeNoTaskContainer is a containerd.Container whose task has disappeared.
// collectContainerMetrics only calls Task() on the stored container; every other
// method is left to the embedded (nil) interface and must not be called.
type fakeNoTaskContainer struct {
	containerd.Container
}

func (fakeNoTaskContainer) Task(context.Context, cio.Attach) (containerd.Task, error) {
	return nil, errdefs.ErrNotFound
}

func TestListPodSandboxMetricsSafety_TransientContainerErrorKeepsPod(t *testing.T) {
	const sandboxID = "sandbox-1"

	c := newTestCRIService()

	sb := sandboxstore.NewSandbox(
		sandboxstore.Metadata{
			ID:     sandboxID,
			Name:   "sandbox-name-1",
			Config: &runtime.PodSandboxConfig{Metadata: &runtime.PodSandboxMetadata{Name: "pod-1"}},
		},
		sandboxstore.Status{State: sandboxstore.StateReady},
	)
	require.NoError(t, c.sandboxStore.Add(sb))

	cntr, err := containerstore.NewContainer(
		containerstore.Metadata{ID: "container-1", SandboxID: sandboxID},
		containerstore.WithContainer(fakeNoTaskContainer{}),
	)
	require.NoError(t, err)
	require.NoError(t, c.containerStore.Add(cntr))

	resp, err := c.ListPodSandboxMetrics(context.Background(), &runtime.ListPodSandboxMetricsRequest{})
	require.NoError(t, err)

	var gotPods []string
	for _, pm := range resp.GetPodMetrics() {
		gotPods = append(gotPods, pm.GetPodSandboxId())
	}

	assert.Contains(t, gotPods, sandboxID,
		"pod %q was dropped from ListPodSandboxMetrics because one container returned a "+
			"transient NotFound; the per-container error path returns nil instead of continue, "+
			"discarding the whole pod's metrics (got pods: %v)", sandboxID, gotPods)
}
