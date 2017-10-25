package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/urfave/cli"
)

var containersSetLabelsCommand = cli.Command{
	Name:        "label",
	Usage:       "set and clear labels for a container.",
	ArgsUsage:   "[flags] <name> [<key>=<value>, ...]",
	Description: "Set and clear labels for a container.",
	Flags:       []cli.Flag{},
	Action: func(context *cli.Context) error {
		containerID, labels := commands.ObjectWithLabelArgs(context)
		if containerID == "" {
			return errors.New("please specify a container")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		container, err := client.LoadContainer(ctx, containerID)
		if err != nil {
			return err
		}

		setlabels, err := container.SetLabels(ctx, labels)
		if err != nil {
			return err
		}

		var labelStrings []string
		for k, v := range setlabels {
			labelStrings = append(labelStrings, fmt.Sprintf("%s=%s", k, v))
		}

		fmt.Println(strings.Join(labelStrings, ","))

		return nil
	},
}
