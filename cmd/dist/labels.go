package main

import (
	"fmt"
	"strings"

	digest "github.com/opencontainers/go-digest"
	"github.com/urfave/cli"
)

var labelCommand = cli.Command{
	Name:        "label",
	Usage:       "adds labels to content",
	ArgsUsage:   "[flags] <digest> [<label>=<value> ...]",
	Description: `Labels blobs in the content store`,
	Flags:       []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			object    = context.Args().First()
			labelArgs = context.Args().Tail()
		)
		ctx, cancel := appContext(context)
		defer cancel()

		cs, err := resolveContentStore(context)
		if err != nil {
			return err
		}

		dgst, err := digest.Parse(object)
		if err != nil {
			return err
		}

		info, err := cs.Info(ctx, dgst)
		if err != nil {
			return err
		}

		if info.Labels == nil {
			info.Labels = map[string]string{}
		}

		var paths []string
		for _, arg := range labelArgs {
			var k, v string
			if idx := strings.IndexByte(arg, '='); idx > 0 {
				k = arg[:idx]
				v = arg[idx+1:]
			} else {
				k = arg
			}
			paths = append(paths, fmt.Sprintf("labels.%s", k))
			if v == "" {
				delete(info.Labels, k)
			} else {
				info.Labels[k] = v
			}
		}

		return cs.Update(ctx, info, paths...)
	},
}
