package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/urfave/cli"
)

var containersSetLabelsCommand = cli.Command{
	Name:        "label",
	Usage:       "set and clear labels for a container.",
	ArgsUsage:   "[flags] <name> [<key>=<value>, ...]",
	Description: "Set and clear labels for a container.",
	Flags:       []cli.Flag{},
	Action: func(clicontext *cli.Context) error {
		var (
			ctx, cancel         = appContext(clicontext)
			containerID, labels = objectWithLabelArgs(clicontext)
		)
		defer cancel()

		client, err := newClient(clicontext)
		if err != nil {
			return err
		}

		if containerID == "" {
			return errors.New("please specify a container")
		}

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
