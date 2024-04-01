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

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	internalapi "github.com/containerd/containerd/v2/integration/cri-api/pkg/apis"
	"github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/log"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

type criWorker struct {
	id       int
	wg       *sync.WaitGroup
	count    int
	failures int
	client   internalapi.RuntimeService

	commit         string
	runtimeHandler string
	snapshotter    string
}

const podNamespaceLabel = "pod.namespace"

func (w *criWorker) incCount() {
	w.count++
}

func (w *criWorker) getCount() int {
	return w.count
}

func (w *criWorker) incFailures() {
	w.failures++
}

func (w *criWorker) getFailures() int {
	return w.failures
}

func (w *criWorker) run(ctx, tctx context.Context) {
	defer func() {
		w.wg.Done()
		log.L.Infof("worker %d finished", w.id)
	}()
	for {
		select {
		case <-tctx.Done():
			return
		default:
		}

		w.count++
		id := w.getID()
		log.L.Debugf("starting container %s", id)
		start := time.Now()
		if err := w.runSandbox(tctx, ctx, id); err != nil {
			if err != context.DeadlineExceeded ||
				!strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
				w.failures++
				log.L.WithError(err).Errorf("running container %s", id)
				errCounter.WithValues(err.Error()).Inc()

			}
			continue
		}
		// only log times are success so we don't scew the results from failures that go really fast
		ct.WithValues(w.commit).UpdateSince(start)
	}
}

func (w *criWorker) runSandbox(tctx, ctx context.Context, id string) (err error) {

	sbConfig := &runtime.PodSandboxConfig{
		Metadata: &runtime.PodSandboxMetadata{
			Name: id,
			// Using random id as uuid is good enough for local
			// integration test.
			Uid:       util.GenerateID(),
			Namespace: "stress",
		},
		Labels: map[string]string{podNamespaceLabel: stressNs},
		Linux:  &runtime.LinuxPodSandboxConfig{},
	}

	sb, err := w.client.RunPodSandbox(sbConfig, w.runtimeHandler)
	if err != nil {
		w.failures++
		return err
	}
	defer func() {
		w.client.StopPodSandbox(sb)
		w.client.RemovePodSandbox(sb)
	}()

	// verify it is running ?

	ticker := time.NewTicker(250 * time.Millisecond)
	go func() {
		for {
			select {
			case <-tctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				// do stuff
				status, err := w.client.PodSandboxStatus(sb)
				if err != nil && status.GetState() == runtime.PodSandboxState_SANDBOX_READY {
					ticker.Stop()
					return
				}
			}
		}
	}()

	return nil
}

func (w *criWorker) getID() string {
	return fmt.Sprintf("%d-%d", w.id, w.count)
}

// cleanup cleans up any containers in the "stress" namespace before the test run
func criCleanup(ctx context.Context, client internalapi.RuntimeService) error {
	filter := &runtime.PodSandboxFilter{
		LabelSelector: map[string]string{podNamespaceLabel: stressNs},
	}

	sandboxes, err := client.ListPodSandbox(filter)
	if err != nil {
		return err
	}

	for _, sb := range sandboxes {
		if err := client.StopPodSandbox(sb.Id); err != nil {
			return err
		}

		if err := client.RemovePodSandbox(sb.Id); err != nil {
			return err
		}
	}

	return nil
}
