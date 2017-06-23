package main

import (
	"fmt"
	"path/filepath"

	"github.com/containerd/containerd"
	tasks "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/linux/runcopts"
	"github.com/gogo/protobuf/proto"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var checkpointCommand = cli.Command{
	Name:      "checkpoint",
	Usage:     "checkpoint a container",
	ArgsUsage: "CONTAINER",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "exit",
			Usage: "stop the container after the checkpoint",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx, cancel = appContext(context)
			id          = context.Args().First()
		)
		defer cancel()

		if id == "" {
			return errors.New("container id must be provided")
		}
		client, err := newClient(context)
		if err != nil {
			return err
		}
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		var opts []containerd.CheckpointOpts
		if context.Bool("exit") {
			opts = append(opts, WithExit)
		}
		checkpoint, err := task.Checkpoint(ctx, opts...)
		if err != nil {
			return err
		}
		fmt.Println(checkpoint.Digest.String())
		return nil
	},
}

func WithExit(r *tasks.CheckpointTaskRequest) error {
	a, err := marshal(&runcopts.CheckpointOptions{
		Exit: true,
	}, "CheckpointOptions")
	if err != nil {
		return err
	}
	r.Options = a
	return nil
}

func marshal(m proto.Message, name string) (*protobuf.Any, error) {
	data, err := proto.Marshal(m)
	if err != nil {
		return nil, err
	}
	return &protobuf.Any{
		TypeUrl: filepath.Join(runcopts.URIBase, name),
		Value:   data,
	}, nil
}
