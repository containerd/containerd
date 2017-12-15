package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/oci"
	metrics "github.com/docker/go-metrics"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sirupsen/logrus"
)

var (
	ct         metrics.LabeledTimer
	errCounter metrics.LabeledCounter
)

func init() {
	ns := metrics.NewNamespace("stress", "", nil)
	// if you want more fine grained metrics then you can drill down with the metrics in prom that
	// containerd is outputing
	ct = ns.NewLabeledTimer("run", "Run time of a full container during the test", "commit")
	errCounter = ns.NewLabeledCounter("errors", "Errors encountered running the stress tests", "err")
	metrics.Register(ns)
}

type worker struct {
	id       int
	wg       *sync.WaitGroup
	count    int
	failures int

	client *containerd.Client
	image  containerd.Image
	spec   *specs.Spec
	doExec bool
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

func (w *worker) runContainer(ctx context.Context, id string) error {
	// fix up cgroups path for a default config
	w.spec.Linux.CgroupsPath = filepath.Join("/", "stress", id)
	c, err := w.client.NewContainer(ctx, id,
		containerd.WithNewSnapshot(id, w.image),
		containerd.WithSpec(w.spec, oci.WithUsername("games")),
	)
	if err != nil {
		return err
	}
	defer c.Delete(ctx, containerd.WithSnapshotCleanup)

	task, err := c.NewTask(ctx, cio.NullIO)
	if err != nil {
		return err
	}
	defer task.Delete(ctx, containerd.WithProcessKill)

	statusC, err := task.Wait(ctx)
	if err != nil {
		return err
	}
	if err := task.Start(ctx); err != nil {
		return err
	}
	if w.doExec {
		for i := 0; i < 256; i++ {
			if err := w.exec(ctx, i, task); err != nil {
				w.failures++
				logrus.WithError(err).Error("exec failure")
			}
		}
		if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
			return err
		}
	}
	status := <-statusC
	_, _, err = status.Result()
	if err != nil {
		if err == context.DeadlineExceeded || err == context.Canceled {
			return nil
		}
		w.failures++
	}
	return nil
}

func (w *worker) exec(ctx context.Context, i int, t containerd.Task) error {
	pSpec := *w.spec.Process
	pSpec.Args = []string{"true"}
	process, err := t.Exec(ctx, strconv.Itoa(i), &pSpec, cio.NullIO)
	if err != nil {
		return err
	}
	defer process.Delete(ctx)
	status, err := process.Wait(ctx)
	if err != nil {
		return err
	}
	if err := process.Start(ctx); err != nil {
		return err
	}
	<-status
	return nil
}

func (w *worker) getID() string {
	return fmt.Sprintf("%d-%d", w.id, w.count)
}
