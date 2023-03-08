//go:build !windows

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

	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"

	"github.com/urfave/cli"
)

func printTaskMetrics(gocontext *cli.Context, metric *types.Metric) error {
	anydata, err := typeurl.UnmarshalAny(metric.Data)
	if err != nil {
		return err
	}

	var (
		data  *v1.Metrics
		data2 *v2.Metrics
	)

	switch v := anydata.(type) {
	case *v1.Metrics:
		data = v
	case *v2.Metrics:
		data2 = v
	default:
		return errors.New("cannot convert metric data to cgroups.Metrics")
	}

	switch gocontext.String(formatFlag) {
	case formatTable:
		w := tabwriter.NewWriter(os.Stdout, 1, 8, 4, ' ', 0)
		fmt.Fprintf(w, "ID\tTIMESTAMP\t\n")
		fmt.Fprintf(w, "%s\t%s\t\n\n", metric.ID, metric.Timestamp)
		if data != nil {
			printCgroupMetricsTable(w, data)
		} else {
			printCgroup2MetricsTable(w, data2)
		}
		return w.Flush()
	case formatJSON:
		marshaledJSON, err := json.MarshalIndent(anydata, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(marshaledJSON))
		return nil
	default:
		return errors.New("format must be table or json")
	}
}

func printCgroup2MetricsTable(w *tabwriter.Writer, data *v2.Metrics) {
	fmt.Fprintf(w, "METRIC\tVALUE\t\n")
	if data.Pids != nil {
		fmt.Fprintf(w, "pids.current\t%v\t\n", data.Pids.Current)
		fmt.Fprintf(w, "pids.limit\t%v\t\n", data.Pids.Limit)
	}
	if data.CPU != nil {
		fmt.Fprintf(w, "cpu.usage_usec\t%v\t\n", data.CPU.UsageUsec)
		fmt.Fprintf(w, "cpu.user_usec\t%v\t\n", data.CPU.UserUsec)
		fmt.Fprintf(w, "cpu.system_usec\t%v\t\n", data.CPU.SystemUsec)
		fmt.Fprintf(w, "cpu.nr_periods\t%v\t\n", data.CPU.NrPeriods)
		fmt.Fprintf(w, "cpu.nr_throttled\t%v\t\n", data.CPU.NrThrottled)
		fmt.Fprintf(w, "cpu.throttled_usec\t%v\t\n", data.CPU.ThrottledUsec)
	}
	if data.Memory != nil {
		fmt.Fprintf(w, "memory.usage\t%v\t\n", data.Memory.Usage)
		fmt.Fprintf(w, "memory.usage_limit\t%v\t\n", data.Memory.UsageLimit)
		fmt.Fprintf(w, "memory.swap_usage\t%v\t\n", data.Memory.SwapUsage)
		fmt.Fprintf(w, "memory.swap_limit\t%v\t\n", data.Memory.SwapLimit)
	}
}
