package main

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/log"
	"github.com/urfave/cli"
)

var containersDeleteCommand = cli.Command{
	Name:      "delete",
	Usage:     "delete an existing container",
	ArgsUsage: "CONTAINER",
	Aliases:   []string{"del", "rm"},
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "keep-snapshot",
			Usage: "do not clean up snapshot with container",
		},
	},
	Action: func(context *cli.Context) error {
		var exitErr error
		ctx, cancel := appContext(context)
		defer cancel()
		client, err := newClient(context)
		if err != nil {
			return err
		}
		deleteOpts := []containerd.DeleteOpts{}
		if !context.Bool("keep-snapshot") {
			deleteOpts = append(deleteOpts, containerd.WithSnapshotCleanup)
		}

		for _, arg := range context.Args() {
			if err := deleteContainer(ctx, client, arg, deleteOpts...); err != nil {
				if exitErr == nil {
					exitErr = err
				}
				log.G(ctx).WithError(err).Errorf("failed to delete container %q", arg)
			}
		}

		return exitErr
	},
}

func deleteContainer(ctx context.Context, client *containerd.Client, id string, opts ...containerd.DeleteOpts) error {
	container, err := client.LoadContainer(ctx, id)
	if err != nil {
		return err
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return container.Delete(ctx, opts...)
	}
	status, err := task.Status(ctx)
	if err != nil {
		return err
	}
	if status == containerd.Stopped || status == containerd.Created {
		if _, err := task.Delete(ctx); err != nil {
			return err
		}
		return container.Delete(ctx, opts...)
	}
	return fmt.Errorf("cannot delete a non stopped container: %v", status)

}
