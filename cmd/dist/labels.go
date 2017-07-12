package main

import (
	"fmt"
	"strings"

	"github.com/containerd/containerd/content"
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
			object, labels = objectWithLabelArgs(context)
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

		info := content.Info{
			Digest: dgst,
			Labels: map[string]string{},
		}

		var paths []string
		for k, v := range labels {
			paths = append(paths, fmt.Sprintf("labels.%s", k))
			if v != "" {
				info.Labels[k] = v
			}
		}

		// Nothing updated, do no clear
		if len(paths) == 0 {
			info, err = cs.Info(ctx, info.Digest)
		} else {
			info, err = cs.Update(ctx, info, paths...)
		}
		if err != nil {
			return err
		}

		var labelStrings []string
		for k, v := range info.Labels {
			labelStrings = append(labelStrings, fmt.Sprintf("%s=%s", k, v))
		}

		fmt.Println(strings.Join(labelStrings, ","))

		return nil
	},
}
