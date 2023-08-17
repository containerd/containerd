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

package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/urfave/cli"
)

const (
	formatFlag  = "format"
	formatTable = "table"
	formatJSON  = "json"
)

var metricsCommand = cli.Command{
	Name:      "metrics",
	Usage:     "Get a single data point of metrics for a task with the built-in Linux runtime",
	ArgsUsage: "CONTAINER",
	Aliases:   []string{"metric"},
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  formatFlag,
			Usage: `"table" or "json"`,
			Value: formatTable,
		},
	},
	Action: func(context *cli.Context) error {
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, context.Args().First())
		if err != nil {
			return err
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return err
		}
		metric, err := task.Metrics(ctx)
		if err != nil {
			return err
		}

		var data interface{}

		if data = allocMetricCgroup1(metric); data == nil {
			if data = allocMetricCgroup2(metric); data == nil {
				if data = allocMetricWstats(metric); data == nil {
					return errors.New("cannot convert metric data to cgroups.Metrics or windows.Statistics")
				}
			}
		}

		switch context.String(formatFlag) {
		case formatTable:
			w := tabwriter.NewWriter(os.Stdout, 1, 8, 4, ' ', 0)
			fmt.Fprintf(w, "ID\tTIMESTAMP\t\n")
			fmt.Fprintf(w, "%s\t%s\t\n\n", metric.ID, metric.Timestamp)
			printCgroup1MetricsTable(w, data)
			printCgroup2MetricsTable(w, data)
			printWindowsStats(w, data)
			return w.Flush()
		case formatJSON:
			marshaledJSON, err := json.MarshalIndent(data, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(marshaledJSON))
			return nil
		default:
			return errors.New("format must be table or json")
		}
	},
}
