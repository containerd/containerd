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

package cgroups

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/containerd/v2/core/metrics/cgroups/common"
	v1 "github.com/containerd/containerd/v2/core/metrics/cgroups/v1"
	v2 "github.com/containerd/containerd/v2/core/metrics/cgroups/v2"
	v1types "github.com/containerd/containerd/v2/core/metrics/types/v1"
	v2types "github.com/containerd/containerd/v2/core/metrics/types/v2"
	"github.com/containerd/containerd/v2/protobuf"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/containerd/containerd/v2/protobuf/types"
	metrics "github.com/docker/go-metrics"
)

// TestRegressionIssue6772 should not have dead-lock when Collect and Add run
// in the same time.
//
// Issue: https://github.com/containerd/containerd/issues/6772.
func TestRegressionIssue6772(t *testing.T) {
	ns := metrics.NewNamespace("test-container", "", nil)
	isV1 := true

	var collecter Collecter
	if cgroups.Mode() == cgroups.Unified {
		isV1 = false
		collecter = v2.NewCollector(ns)
	} else {
		collecter = v1.NewCollector(ns)
	}

	doneCh := make(chan struct{})
	defer close(doneCh)

	maxItem := 100
	startCh := make(chan struct{})

	metricCh := make(chan prometheus.Metric, maxItem)

	go func() {
		for {
			select {
			case <-doneCh:
				return
			case <-metricCh:
			}
		}
	}()

	go func() {
		// pulling the metrics to trigger dead-lock
		ns.Collect(metricCh)
		close(startCh)

		for {
			select {
			case <-doneCh:
				return
			default:
			}

			ns.Collect(metricCh)
		}
	}()
	<-startCh

	labels := map[string]string{"issue": "6772"}
	errCh := make(chan error, 1)

	var wg sync.WaitGroup
	for i := 0; i < maxItem; i++ {
		id := i
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := collecter.Add(
				&mockStatT{
					id:        strconv.Itoa(id),
					namespace: "issue6772",
					isV1:      isV1,
				},
				labels,
			)
			if err != nil {
				errCh <- err
			}
		}()
	}

	finishedCh := make(chan struct{})
	go func() {
		defer close(finishedCh)

		wg.Wait()
	}()

	select {
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-finishedCh:
	case <-time.After(30 * time.Second):
		t.Fatal("should finish the Add in time")
	}
}

type Collecter interface {
	Collect(ch chan<- prometheus.Metric)

	Add(t common.Statable, labels map[string]string) error
}

type mockStatT struct {
	id, namespace string
	isV1          bool
}

func (t *mockStatT) ID() string {
	return t.id
}

func (t *mockStatT) Namespace() string {
	return t.namespace
}

func (t *mockStatT) Stats(context.Context) (*types.Any, error) {
	if t.isV1 {
		return protobuf.MarshalAnyToProto(&v1types.Metrics{})
	}
	return protobuf.MarshalAnyToProto(&v2types.Metrics{})
}
