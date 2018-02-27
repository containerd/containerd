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
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	metrics "github.com/docker/go-metrics"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const imageName = "docker.io/library/alpine:latest"

var (
	ct              metrics.LabeledTimer
	execTimer       metrics.LabeledTimer
	errCounter      metrics.LabeledCounter
	binarySizeGauge metrics.LabeledGauge
)

func init() {
	ns := metrics.NewNamespace("stress", "", nil)
	// if you want more fine grained metrics then you can drill down with the metrics in prom that
	// containerd is outputing
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

func (r *run) gather(workers []*worker) *result {
	for _, w := range workers {
		r.total += w.count
		r.failures += w.failures
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
	// morr power!
	runtime.GOMAXPROCS(runtime.NumCPU())

	app := cli.NewApp()
	app.Name = "containerd-stress"
	app.Description = "stress test a containerd daemon"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "set debug output in the logs",
		},
		cli.StringFlag{
			Name:  "address,a",
			Value: "/run/containerd/containerd.sock",
			Usage: "path to the containerd socket",
		},
		cli.IntFlag{
			Name:  "concurrent,c",
			Value: 1,
			Usage: "set the concurrency of the stress test",
		},
		cli.DurationFlag{
			Name:  "duration,d",
			Value: 1 * time.Minute,
			Usage: "set the duration of the stress test",
		},
		cli.BoolFlag{
			Name:  "exec",
			Usage: "add execs to the stress tests",
		},
		cli.BoolFlag{
			Name:  "json,j",
			Usage: "output results in json format",
		},
		cli.StringFlag{
			Name:  "metrics,m",
			Usage: "address to serve the metrics API",
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
			Exec:        context.GlobalBool("exec"),
			JSON:        context.GlobalBool("json"),
			Metrics:     context.GlobalString("metrics"),
		}
		if config.Metrics != "" {
			return serve(config)
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
	Duration    time.Duration
	Address     string
	Exec        bool
	JSON        bool
	Metrics     string
}

func (c config) newClient() (*containerd.Client, error) {
	return containerd.New(c.Address)
}

func serve(c config) error {
	go func() {
		if err := http.ListenAndServe(c.Metrics, metrics.Handler()); err != nil {
			logrus.WithError(err).Error("listen and serve")
		}
	}()
	checkBinarySizes()
	return test(c)
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
	logrus.Infof("pulling %s", imageName)
	image, err := client.Pull(ctx, imageName, containerd.WithPullUnpack)
	if err != nil {
		return err
	}
	logrus.Info("generating spec from image")
	tctx, cancel := context.WithTimeout(ctx, c.Duration)
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGTERM, syscall.SIGINT)
		<-s
		cancel()
	}()

	var (
		workers []*worker
		r       = &run{}
	)
	logrus.Info("starting stress test run...")
	args := oci.WithProcessArgs("true")
	v, err := client.Version(ctx)
	if err != nil {
		return err
	}
	// create the workers along with their spec
	for i := 0; i < c.Concurrency; i++ {
		wg.Add(1)
		spec, err := oci.GenerateSpec(ctx, client,
			&containers.Container{},
			oci.WithImageConfig(image),
			args,
		)
		if err != nil {
			return err
		}
		w := &worker{
			id:     i,
			wg:     &wg,
			spec:   spec,
			image:  image,
			client: client,
			commit: v.Revision,
		}
		workers = append(workers, w)
	}
	var exec *execWorker
	if c.Exec {
		wg.Add(1)
		spec, err := oci.GenerateSpec(ctx, client,
			&containers.Container{},
			oci.WithImageConfig(image),
			args,
		)
		if err != nil {
			return err
		}
		exec = &execWorker{
			worker: worker{
				id:     c.Concurrency,
				wg:     &wg,
				spec:   spec,
				image:  image,
				client: client,
				commit: v.Revision,
			},
		}
		go exec.exec(ctx, tctx)
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
