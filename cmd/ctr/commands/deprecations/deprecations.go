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

package deprecations

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v2"

	api "github.com/containerd/containerd/api/services/introspection/v1"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/pkg/protobuf"
)

// Command is the parent for all commands under "deprecations"
var Command = &cli.Command{
	Name: "deprecations",
	Subcommands: []*cli.Command{
		listCommand,
	},
}
var listCommand = &cli.Command{
	Name:  "list",
	Usage: "Print warnings for deprecations",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "format",
			Usage: "output format to use (Examples: 'default', 'json')",
		},
	},
	Action: func(cliContext *cli.Context) error {
		// Suppress automatic warnings, since we print the warnings by ourselves.
		os.Setenv("CONTAINERD_SUPPRESS_DEPRECATION_WARNINGS", "1")

		client, ctx, cancel, err := commands.NewClient(cliContext)
		if err != nil {
			return err
		}
		defer cancel()

		resp, err := client.IntrospectionService().Server(ctx)
		if err != nil {
			return err
		}
		wrn := warnings(resp)
		if len(wrn) > 0 {
			switch cliContext.String("format") {
			case "json":
				commands.PrintAsJSON(warnings(resp))
				return nil
			default:
				w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
				fmt.Fprintln(w, "ID\tLAST OCCURRENCE\tMESSAGE\t")
				for _, dw := range wrn {
					if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n",
						dw.ID,
						dw.LastOccurrence.Format(time.RFC3339Nano),
						dw.Message,
					); err != nil {
						return err
					}
				}
				return w.Flush()
			}

		}
		return nil
	},
}

type deprecationWarning struct {
	ID             string    `json:"id"`
	Message        string    `json:"message"`
	LastOccurrence time.Time `json:"lastOccurrence"`
}

func warnings(in *api.ServerResponse) []deprecationWarning {
	var warnings []deprecationWarning
	for _, dw := range in.Deprecations {
		wrn := deprecationWarningFromPB(dw)
		if wrn == nil {
			continue
		}
		warnings = append(warnings, *wrn)
	}
	return warnings
}
func deprecationWarningFromPB(in *api.DeprecationWarning) *deprecationWarning {
	if in == nil {
		return nil
	}
	lo := protobuf.FromTimestamp(in.LastOccurrence)
	return &deprecationWarning{
		ID:             in.ID,
		Message:        in.Message,
		LastOccurrence: lo,
	}
}
