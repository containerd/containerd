/*
Copyright 2018 The containerd Authors.

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

package cli

import (
	gocontext "context"
	"fmt"
	"path/filepath"

	api "github.com/containerd/cri/pkg/api/v1"
	"github.com/containerd/cri/pkg/client"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

// Command is the cli command for cri plugin.
var Command = cli.Command{
	Name:  "cri",
	Usage: "interact with cri plugin",
	Subcommands: cli.Commands{
		loadCommand,
	},
}

var loadCommand = cli.Command{
	Name:        "load",
	Usage:       "load one or more images from tar archives.",
	ArgsUsage:   "[flags] TAR [TAR, ...]",
	Description: "load one or more images from tar archives.",
	Flags:       []cli.Flag{},
	Action: func(context *cli.Context) error {
		var (
			ctx     = gocontext.Background()
			address = context.GlobalString("address")
			timeout = context.GlobalDuration("timeout")
			cancel  gocontext.CancelFunc
		)
		cl, err := client.NewCRIContainerdClient(address, timeout)
		if err != nil {
			return fmt.Errorf("failed to create grpc client: %v", err)
		}
		if timeout > 0 {
			ctx, cancel = gocontext.WithTimeout(gocontext.Background(), timeout)
		} else {
			ctx, cancel = gocontext.WithCancel(ctx)
		}
		defer cancel()
		for _, path := range context.Args() {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return errors.Wrap(err, "failed to get absolute path")
			}
			res, err := cl.LoadImage(ctx, &api.LoadImageRequest{FilePath: absPath})
			if err != nil {
				return errors.Wrap(err, "failed to load image")
			}
			images := res.GetImages()
			for _, image := range images {
				fmt.Println("Loaded image:", image)
			}
		}
		return nil
	},
}
