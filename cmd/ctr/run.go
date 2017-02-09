package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	gocontext "context"

	"github.com/crosbymichael/console"
	"github.com/docker/containerd/api/services/execution"
	execEvents "github.com/docker/containerd/execution"
	"github.com/nats-io/go-nats"
	"github.com/nats-io/go-nats-streaming"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var runCommand = cli.Command{
	Name:  "run",
	Usage: "run a container",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "bundle, b",
			Usage: "path to the container's bundle",
		},
		cli.BoolFlag{
			Name:  "tty, t",
			Usage: "allocate a TTY for the container",
		},
	},
	Action: func(context *cli.Context) error {
		id := context.Args().First()
		if id == "" {
			return fmt.Errorf("container id must be provided")
		}
		executionService, err := getExecutionService(context)
		if err != nil {
			return err
		}

		// setup our event subscriber
		sc, err := stan.Connect("containerd", "ctr", stan.ConnectWait(5*time.Second))
		if err != nil {
			return err
		}
		defer sc.Close()

		evCh := make(chan *execEvents.ContainerEvent, 64)
		sub, err := sc.Subscribe(fmt.Sprintf("containers.%s", id), func(m *stan.Msg) {
			var e execEvents.ContainerEvent

			err := json.Unmarshal(m.Data, &e)
			if err != nil {
				fmt.Printf("failed to unmarshal event: %v", err)
				return
			}

			evCh <- &e
		})
		if err != nil {
			return err
		}
		defer sub.Unsubscribe()

		tmpDir, err := getTempDir(id)
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)

		bundle, err := filepath.Abs(context.String("bundle"))
		if err != nil {
			return err
		}
		crOpts := &execution.CreateContainerRequest{
			ID:         id,
			BundlePath: bundle,
			Console:    context.Bool("tty"),
			Stdin:      filepath.Join(tmpDir, "stdin"),
			Stdout:     filepath.Join(tmpDir, "stdout"),
			Stderr:     filepath.Join(tmpDir, "stderr"),
		}

		if crOpts.Console {
			con := console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}

		fwg, err := prepareStdio(crOpts.Stdin, crOpts.Stdout, crOpts.Stderr, crOpts.Console)
		if err != nil {
			return err
		}

		cr, err := executionService.CreateContainer(gocontext.Background(), crOpts)
		if err != nil {
			return errors.Wrap(err, "CreateContainer RPC failed")
		}

		if _, err := executionService.StartContainer(gocontext.Background(), &execution.StartContainerRequest{
			ID: cr.Container.ID,
		}); err != nil {
			return errors.Wrap(err, "StartContainer RPC failed")
		}

		var ec uint32
	eventLoop:
		for {
			select {
			case e, more := <-evCh:
				if !more {
					break eventLoop
				}

				if e.Type != "exit" {
					continue
				}

				if e.ID == cr.Container.ID && e.Pid == cr.InitProcess.Pid {
					ec = e.ExitStatus
					break eventLoop
				}
			case <-time.After(1 * time.Second):
				if sc.NatsConn().Status() != nats.CONNECTED {
					break eventLoop
				}
			}
		}

		if _, err := executionService.DeleteContainer(gocontext.Background(), &execution.DeleteContainerRequest{
			ID: cr.Container.ID,
		}); err != nil {
			return errors.Wrap(err, "DeleteContainer RPC failed")
		}

		// Ensure we read all io
		fwg.Wait()

		if ec != 0 {
			return cli.NewExitError("", int(ec))
		}

		return nil
	},
}
