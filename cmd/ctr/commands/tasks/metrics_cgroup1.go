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
	"fmt"
	"text/tabwriter"

	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"
)

func printCgroup1MetricsTable(w *tabwriter.Writer, data interface{}) {
	switch data := data.(type) {
	case *v1.Metrics:
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
}

func allocMetricCgroup1(metric *types.Metric) interface{} {
	if typeurl.Is(metric.Data, (*v1.Metrics)(nil)) {
		return &v1.Metrics{}
	}
	return nil
}
