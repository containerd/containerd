package main

import (
	"fmt"

	"github.com/codegangsta/cli"
)

var checkpointSubCmds = []cli.Command{
	listCheckpointCommand,
}

var checkpointCommand = cli.Command{
	Name:        "checkpoints",
	Usage:       "list all checkpoints",
	ArgsUsage:   "COMMAND [arguments...]",
	Subcommands: checkpointSubCmds,
	Description: func() string {
		desc := "\n    COMMAND:\n"
		for _, command := range checkpointSubCmds {
			desc += fmt.Sprintf("    %-10.10s%s\n", command.Name, command.Usage)
		}
		return desc
	}(),
	Action: listCheckpoints,
}

var listCheckpointCommand = cli.Command{
	Name:   "list",
	Usage:  "list all checkpoints for a container",
	Action: listCheckpoints,
}

func listCheckpoints(context *cli.Context) {
	fatal("checkpoint command is not supported on Solaris", ExitStatusUnsupported)
}
