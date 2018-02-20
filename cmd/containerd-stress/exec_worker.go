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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

type execWorker struct {
	worker
}

func (w *execWorker) exec(ctx, tctx context.Context) {
	defer func() {
		w.wg.Done()
		logrus.Infof("worker %d finished", w.id)
	}()
	// create and start the exec container
	w.spec.Linux.CgroupsPath = filepath.Join("/", "stress", "exec-container")
	w.spec.Process.Args = []string{
		"sleep", "30d",
	}
	c, err := w.client.NewContainer(ctx, "exec-container",
		containerd.WithNewSnapshot("exec-container", w.image),
		containerd.WithSpec(w.spec, oci.WithUsername("games")),
	)
	if err != nil {
		logrus.WithError(err).Error("create exec container")
		return
	}
	defer c.Delete(ctx, containerd.WithSnapshotCleanup)

	task, err := c.NewTask(ctx, cio.NullIO)
	if err != nil {
		logrus.WithError(err).Error("create exec container's task")
		return
	}
	defer task.Delete(ctx, containerd.WithProcessKill)

	statusC, err := task.Wait(ctx)
	if err != nil {
		logrus.WithError(err).Error("wait exec container's task")
		return
	}

	pspec := w.spec.Process
	pspec.Args = []string{"true"}

	for {
		select {
		case <-tctx.Done():
			if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
				logrus.WithError(err).Error("kill exec container's task")
			}
			<-statusC
			return
		default:
		}

		w.count++
		id := w.getID()
		logrus.Debugf("starting exec %s", id)
		start := time.Now()

		if err := w.runExec(ctx, task, id, pspec); err != nil {
			if err != context.DeadlineExceeded ||
				!strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
				w.failures++
				logrus.WithError(err).Errorf("running exec %s", id)
				errCounter.WithValues(err.Error()).Inc()
			}
			continue
		}
		// only log times are success so we don't scew the results from failures that go really fast
		execTimer.WithValues(w.commit).UpdateSince(start)
	}
}

func (w *execWorker) runExec(ctx context.Context, task containerd.Task, id string, spec *specs.Process) (err error) {
	process, err := task.Exec(ctx, id, spec, cio.NullIO)
	if err != nil {
		return err
	}
	defer func() {
		if _, derr := process.Delete(ctx, containerd.WithProcessKill); err == nil {
			err = derr
		}
	}()
	statusC, err := process.Wait(ctx)
	if err != nil {
		return err
	}
	if err := process.Start(ctx); err != nil {
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
