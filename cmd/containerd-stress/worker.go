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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

type worker struct {
	id       int
	wg       *sync.WaitGroup
	count    int
	failures int

	client *containerd.Client
	image  containerd.Image
	spec   *specs.Spec
	commit string
}

func (w *worker) run(ctx, tctx context.Context) {
	defer func() {
		w.wg.Done()
		logrus.Infof("worker %d finished", w.id)
	}()
	for {
		select {
		case <-tctx.Done():
			return
		default:
		}

		w.count++
		id := w.getID()
		logrus.Debugf("starting container %s", id)
		start := time.Now()
		if err := w.runContainer(ctx, id); err != nil {
			if err != context.DeadlineExceeded ||
				!strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
				w.failures++
				logrus.WithError(err).Errorf("running container %s", id)
				errCounter.WithValues(err.Error()).Inc()

			}
			continue
		}
		// only log times are success so we don't scew the results from failures that go really fast
		ct.WithValues(w.commit).UpdateSince(start)
	}
}

func (w *worker) runContainer(ctx context.Context, id string) (err error) {
	// fix up cgroups path for a default config
	w.spec.Linux.CgroupsPath = filepath.Join("/", "stress", id)
	c, err := w.client.NewContainer(ctx, id,
		containerd.WithNewSnapshot(id, w.image),
		containerd.WithSpec(w.spec, oci.WithUsername("games")),
	)
	if err != nil {
		return err
	}
	defer func() {
		if derr := c.Delete(ctx, containerd.WithSnapshotCleanup); err == nil {
			err = derr
		}
	}()
	task, err := c.NewTask(ctx, cio.NullIO)
	if err != nil {
		return err
	}
	defer func() {
		if _, derr := task.Delete(ctx, containerd.WithProcessKill); err == nil {
			err = derr
		}
	}()
	statusC, err := task.Wait(ctx)
	if err != nil {
		return err
	}
	if err := task.Start(ctx); err != nil {
		return err
	}
	status := <-statusC
	_, _, err = status.Result()
	if err != nil {
		if err == context.DeadlineExceeded || err == context.Canceled {
			return nil
		}
		w.failures++
		errCounter.WithValues(err.Error()).Inc()
	}
	return nil
}

func (w *worker) getID() string {
	return fmt.Sprintf("%d-%d", w.id, w.count)
}
