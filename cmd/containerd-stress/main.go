package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const imageName = "docker.io/library/alpine:latest"

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
	Seconds             float64 `json:"seconds"`
	ContainersPerSecond float64 `json:"containersPerSecond"`
	SecondsPerContainer float64 `json:"secondsPerContainer"`
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
	}
	app.Before = func(context *cli.Context) error {
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		if context.GlobalBool("json") {
			logrus.SetLevel(logrus.WarnLevel)
		}
		return nil
	}
	app.Action = func(context *cli.Context) error {
		config := config{
			Address:     context.GlobalString("address"),
			Duration:    context.GlobalDuration("duration"),
			Concurrency: context.GlobalInt("concurrent"),
			Exec:        context.GlobalBool("exec"),
			Json:        context.GlobalBool("json"),
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
	Json        bool
}

func (c config) newClient() (*containerd.Client, error) {
	return containerd.New(c.Address)
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
	if c.Exec {
		args = oci.WithProcessArgs("sleep", "10")
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
			doExec: c.Exec,
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
	if c.Json {
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
