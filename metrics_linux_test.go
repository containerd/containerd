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

package containerd

import (
	"testing"

	metricv1 "github.com/containerd/containerd/metrics/types/v1"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/typeurl"
)

func TestMetricsLoadCgroupstatsRuncShimV2(t *testing.T) {
	t.Parallel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var (
		image       Image
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err = client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(
		ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(oci.WithImageConfig(image), withProcessArgs("sleep", "999")),
		WithRuntime(plugin.RuntimeRuncV2, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty(), WithLoadCgroupstats(true))
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx, WithProcessKill)

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	mc, err := task.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := typeurl.UnmarshalAny(mc.Data)
	if err != nil {
		t.Fatal(err)
	}

	var nrSleeping, nrRunning uint64 = 1, 0
	cgstats := stats.(*metricv1.Metrics).CgroupStats
	if cgstats == nil {
		t.Fatal("expect cgroupstats in metrics, but got nil")
	}

	if cgstats.NrSleeping != nrSleeping || cgstats.NrRunning != nrRunning {
		t.Fatalf("expected nrRunning=%d nrSleeping=%d, but got nrRunning=%d, nrSleeping=%d",
			nrRunning, nrSleeping, cgstats.NrRunning, cgstats.NrSleeping)
	}
}
