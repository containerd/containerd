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

package info

import (
	api "github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/containerd/containerd/cmd/ctr/commands"
	ptypes "github.com/containerd/containerd/protobuf/types"
	"github.com/urfave/cli"
)

type Info struct {
	Server *api.ServerResponse `json:"server"`
}

// Command is a cli command to output the containerd server info
var Command = cli.Command{
	Name:  "info",
	Usage: "Print the server info",
	Action: func(context *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		var info Info
		info.Server, err = client.IntrospectionService().Server(ctx, &ptypes.Empty{})
		if err != nil {
			return err
		}
		commands.PrintAsJSON(info)
		return nil
	},
}
