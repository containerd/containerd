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
	"errors"
	"fmt"
	"text/tabwriter"

	v1 "github.com/containerd/cgroups/stats/v1"
	v2 "github.com/containerd/cgroups/v2/stats"
)

func printMetricsTable(w *tabwriter.Writer, anydata interface{}) error {
	switch v := anydata.(type) {
	case *v1.Metrics:
		printCgroupV1MetricsTable(w, v)
	case *v2.Metrics:
		printCgroupV2MetricsTable(w, v)
	default:
		return errors.New("cannot convert metric data to cgroups.Metrics")
	}

	return nil
}

func printCgroupV1MetricsTable(w *tabwriter.Writer, data *v1.Metrics) {
	fmt.Fprintf(w, "METRIC\tVALUE\t\n")
	if data.Memory != nil {
		fmt.Fprintf(w, "memory.usage_in_bytes\t%d\t\n", data.Memory.Usage.Usage)
		fmt.Fprintf(w, "memory.limit_in_bytes\t%d\t\n", data.Memory.Usage.Limit)
		fmt.Fprintf(w, "memory.stat.cache\t%d\t\n", data.Memory.TotalCache)
	}
	if data.CPU != nil {
		fmt.Fprintf(w, "cpuacct.usage\t%d\t\n", data.CPU.Usage.Total)
		fmt.Fprintf(w, "cpuacct.usage_percpu\t%v\t\n", data.CPU.Usage.PerCPU)
	}
	if data.Pids != nil {
		fmt.Fprintf(w, "pids.current\t%v\t\n", data.Pids.Current)
		fmt.Fprintf(w, "pids.limit\t%v\t\n", data.Pids.Limit)
	}
}

func printCgroupV2MetricsTable(w *tabwriter.Writer, data *v2.Metrics) {
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
