/*
   Copyright The containerd Authors.

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

package oci

import (
	"fmt"

	"github.com/urfave/cli"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/platforms"
)

// Command is the parent for all OCI related tools under 'oci'
var Command = cli.Command{
	Name:  "oci",
	Usage: "OCI tools",
	Subcommands: []cli.Command{
		defaultSpecCommand,
	},
}

var defaultSpecCommand = cli.Command{
	Name:  "spec",
	Usage: "See the output of the default OCI spec",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "platform",
			Usage: "Platform of the spec to print (Examples: 'linux/arm64', 'windows/amd64')",
		},
	},
	Action: func(context *cli.Context) error {
		ctx, cancel := commands.AppContext(context)
		defer cancel()

		platform := platforms.DefaultString()
		if plat := context.String("platform"); plat != "" {
			platform = plat
		}

		spec, err := oci.GenerateSpecWithPlatform(ctx, nil, platform, &containers.Container{})
		if err != nil {
			return fmt.Errorf("failed to generate spec: %w", err)
		}

		commands.PrintAsJSON(spec)
		return nil
	},
}
