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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/integration/remote"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/plugin"
	metrics "github.com/docker/go-metrics"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	ct              metrics.LabeledTimer
	execTimer       metrics.LabeledTimer
	errCounter      metrics.LabeledCounter
	binarySizeGauge metrics.LabeledGauge
)

const (
	stressNs string = "stress"
)

func init() {
	ns := metrics.NewNamespace(stressNs, "", nil)
	// if you want more fine grained metrics then you can drill down with the metrics in prom that
	// containerd is outputting
	ct = ns.NewLabeledTimer("run", "Run time of a full container during the test", "commit")
	execTimer = ns.NewLabeledTimer("exec", "Run time of an exec process during the test", "commit")
	binarySizeGauge = ns.NewLabeledGauge("binary_size", "Binary size of compiled binaries", metrics.Bytes, "name")
	errCounter = ns.NewLabeledCounter("errors", "Errors encountered running the stress tests", "err")
	metrics.Register(ns)

	// set higher ulimits
	if err := setRlimit(); err != nil {
		panic(err)
	}
}

type worker interface {
	run(ctx, tcxt context.Context)
	getCount() int
	incCount()
	getFailures() int
	incFailures()
}

type run struct {
	total    int
	failures int

	started time.Time
	ended   time.Time
}

func (r *run) start() {
	r.started = time.Now()
}

func (r *run) end() {
	r.ended = time.Now()
}

func (r *run) seconds() float64 {
	return r.ended.Sub(r.started).Seconds()
}

func (r *run) gather(workers []worker) *result {
	for _, w := range workers {
		r.total += w.getCount()
		r.failures += w.getFailures()
	}
	sec := r.seconds()
	return &result{
		Total:               r.total,
		Seconds:             sec,
		ContainersPerSecond: float64(r.total) / sec,
		SecondsPerContainer: sec / float64(r.total),
	}
}

type result struct {
	Total               int     `json:"total"`
	Failures            int     `json:"failures"`
	Seconds             float64 `json:"seconds"`
	ContainersPerSecond float64 `json:"containersPerSecond"`
	SecondsPerContainer float64 `json:"secondsPerContainer"`
	ExecTotal           int     `json:"execTotal"`
	ExecFailures        int     `json:"execFailures"`
}

func main() {
	// more power!
	runtime.GOMAXPROCS(runtime.NumCPU())

	app := cli.NewApp()
	app.Name = "containerd-stress"
	app.Description = "stress test a containerd daemon"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "Set debug output in the logs",
		},
		cli.StringFlag{
			Name:  "address,a",
			Value: "/run/containerd/containerd.sock",
			Usage: "Path to the containerd socket",
		},
		cli.IntFlag{
			Name:  "concurrent,c",
			Value: 1,
			Usage: "Set the concurrency of the stress test",
		},
		cli.BoolFlag{
			Name:  "cri",
			Usage: "Utilize CRI to create pods for the stress test. This requires a runtime that matches CRI runtime handler. Example: --runtime runc",
		},
		cli.DurationFlag{
			Name:  "duration,d",
			Value: 1 * time.Minute,
			Usage: "Set the duration of the stress test",
		},
		cli.BoolFlag{
			Name:  "exec",
			Usage: "Add execs to the stress tests (non-CRI only)",
		},
		cli.StringFlag{
			Name:  "image,i",
			Value: "docker.io/library/alpine:latest",
			Usage: "Image to be utilized for testing",
		},
		cli.BoolFlag{
			Name:  "json,j",
			Usage: "Output results in json format",
		},
		cli.StringFlag{
			Name:  "metrics,m",
			Usage: "Address to serve the metrics API",
		},
		cli.StringFlag{
			Name:  "runtime",
			Usage: "Set the runtime to stress test",
			Value: plugin.RuntimeRuncV2,
		},
		cli.StringFlag{
			Name:  "snapshotter",
			Usage: "Set the snapshotter to use",
			Value: "overlayfs",
		},
	}
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("json") {
			logrus.SetLevel(logrus.WarnLevel)
		}
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	app.Commands = []cli.Command{
		densityCommand,
	}
	app.Action = func(context *cli.Context) error {
		config := config{
			Address:     context.GlobalString("address"),
			Duration:    context.GlobalDuration("duration"),
			Concurrency: context.GlobalInt("concurrent"),
			CRI:         context.GlobalBool("cri"),
			Exec:        context.GlobalBool("exec"),
			Image:       context.GlobalString("image"),
			JSON:        context.GlobalBool("json"),
			Metrics:     context.GlobalString("metrics"),
			Runtime:     context.GlobalString("runtime"),
			Snapshotter: context.GlobalString("snapshotter"),
		}
		if config.Metrics != "" {
			return serve(config)
		}

		if config.CRI {
			return criTest(config)
		}

		return test(config)
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type config struct {
	Concurrency int
	CRI         bool
	Duration    time.Duration
	Address     string
	Exec        bool
	Image       string
	JSON        bool
	Metrics     string
	Runtime     string
	Snapshotter string
}

func (c config) newClient() (*containerd.Client, error) {
	return containerd.New(c.Address, containerd.WithDefaultRuntime(c.Runtime))
}

func serve(c config) error {
	go func() {
		srv := &http.Server{
			Addr:              c.Metrics,
			Handler:           metrics.Handler(),
			ReadHeaderTimeout: 5 * time.Minute, // "G112: Potential Slowloris Attack (gosec)"; not a real concern for our use, so setting a long timeout.
		}
		if err := srv.ListenAndServe(); err != nil {
			logrus.WithError(err).Error("listen and serve")
		}
	}()
	checkBinarySizes()

	if c.CRI {
		return criTest(c)
	}

	return test(c)
}

func criTest(c config) error {
	var (
		timeout     = 1 * time.Minute
		wg          sync.WaitGroup
		ctx         = namespaces.WithNamespace(context.Background(), stressNs)
		criEndpoint = "unix:///run/containerd/containerd.sock"
	)

	client, err := remote.NewRuntimeService(criEndpoint, timeout)
	if err != nil {
		return fmt.Errorf("failed to create runtime service: %w", err)
	}

	if err := criCleanup(ctx, client); err != nil {
		return err
	}

	tctx, cancel := context.WithTimeout(ctx, c.Duration)
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGTERM, syscall.SIGINT)
		<-s
		cancel()
	}()

	// get runtime version:
	version, err := client.Version("")
	if err != nil {
		return fmt.Errorf("failed to get runtime version: %w", err)
	}
	var (
		workers []worker
		r       = &run{}
	)
	logrus.Info("starting stress test run...")
	// create the workers along with their spec
	for i := 0; i < c.Concurrency; i++ {
		wg.Add(1)
		w := &criWorker{
			id:             i,
			wg:             &wg,
			client:         client,
			commit:         fmt.Sprintf("%s-%s", version.RuntimeName, version.RuntimeVersion),
			runtimeHandler: c.Runtime,
			snapshotter:    c.Snapshotter,
		}
		workers = append(workers, w)
	}

	// start the timer and run the worker
	r.start()
	for _, w := range workers {
		go w.run(ctx, tctx)
	}
	// wait and end the timer
	wg.Wait()
	r.end()

	results := r.gather(workers)
	logrus.Infof("ending test run in %0.3f seconds", results.Seconds)

	logrus.WithField("failures", r.failures).Infof(
		"create/start/delete %d containers in %0.3f seconds (%0.3f c/sec) or (%0.3f sec/c)",
		results.Total,
		results.Seconds,
		results.ContainersPerSecond,
		results.SecondsPerContainer,
	)
	if c.JSON {
		if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
			return err
		}
	}

	return nil
}

func test(c config) error {
	var (
		wg  sync.WaitGroup
		ctx = namespaces.WithNamespace(context.Background(), "stress")
	)

	client, err := c.newClient()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := cleanup(ctx, client); err != nil {
		return err
	}

	logrus.Infof("pulling %s", c.Image)
	image, err := client.Pull(ctx, c.Image, containerd.WithPullUnpack, containerd.WithPullSnapshotter(c.Snapshotter))
	if err != nil {
		return err
	}
	v, err := client.Version(ctx)
	if err != nil {
		return err
	}

	tctx, cancel := context.WithTimeout(ctx, c.Duration)
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGTERM, syscall.SIGINT)
		<-s
		cancel()
	}()

	var (
		workers []worker
		r       = &run{}
	)
	logrus.Info("starting stress test run...")
	// create the workers along with their spec
	for i := 0; i < c.Concurrency; i++ {
		wg.Add(1)

		w := &ctrWorker{
			id:          i,
			wg:          &wg,
			image:       image,
			client:      client,
			commit:      v.Revision,
			snapshotter: c.Snapshotter,
		}
		workers = append(workers, w)
	}
	var exec *execWorker
	if c.Exec {
		for i := c.Concurrency; i < c.Concurrency+c.Concurrency; i++ {
			wg.Add(1)
			exec = &execWorker{
				ctrWorker: ctrWorker{
					id:          i,
					wg:          &wg,
					image:       image,
					client:      client,
					commit:      v.Revision,
					snapshotter: c.Snapshotter,
				},
			}
			go exec.exec(ctx, tctx)
		}
	}

	// start the timer and run the worker
	r.start()
	for _, w := range workers {
		go w.run(ctx, tctx)
	}
	// wait and end the timer
	wg.Wait()
	r.end()

	results := r.gather(workers)
	if c.Exec {
		results.ExecTotal = exec.count
		results.ExecFailures = exec.failures
	}
	logrus.Infof("ending test run in %0.3f seconds", results.Seconds)

	logrus.WithField("failures", r.failures).Infof(
		"create/start/delete %d containers in %0.3f seconds (%0.3f c/sec) or (%0.3f sec/c)",
		results.Total,
		results.Seconds,
		results.ContainersPerSecond,
		results.SecondsPerContainer,
	)
	if c.JSON {
		if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
			return err
		}
	}
	return nil
}

// cleanup cleans up any containers in the "stress" namespace before the test run
func cleanup(ctx context.Context, client *containerd.Client) error {
	containers, err := client.Containers(ctx)
	if err != nil {
		return err
	}
	for _, c := range containers {
		task, err := c.Task(ctx, nil)
		if err == nil {
			task.Delete(ctx, containerd.WithProcessKill)
		}
		if err := c.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			if derr := c.Delete(ctx); derr == nil {
				continue
			}
			return err
		}
	}
	return nil
}
