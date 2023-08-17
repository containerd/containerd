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

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/typeurl/v2"
)

func printWindowsStats(w *tabwriter.Writer, windowsStats interface{}) {
	switch windowsStats := windowsStats.(type) {
	case *wstats.Statistics:
		if windowsStats.GetLinux() != nil {
			stats := windowsStats.GetLinux()
			printCgroup1MetricsTable(w, stats)
		} else if windowsStats.GetWindows() != nil {
			printWindowsContainerStatistics(w, windowsStats.GetWindows())
		}
		// Print VM stats if its isolated
		if windowsStats.VM != nil {
			printWindowsVMStatistics(w, windowsStats.VM)
		}
	}
}

func printWindowsContainerStatistics(w *tabwriter.Writer, stats *wstats.WindowsContainerStatistics) {
	fmt.Fprintf(w, "METRIC\tVALUE\t\n")
	fmt.Fprintf(w, "timestamp\t%s\t\n", stats.Timestamp)
	fmt.Fprintf(w, "start_time\t%s\t\n", stats.ContainerStartTime)
	fmt.Fprintf(w, "uptime_ns\t%d\t\n", stats.UptimeNS)
	if stats.Processor != nil {
		fmt.Fprintf(w, "cpu.total_runtime_ns\t%d\t\n", stats.Processor.TotalRuntimeNS)
		fmt.Fprintf(w, "cpu.runtime_user_ns\t%d\t\n", stats.Processor.RuntimeUserNS)
		fmt.Fprintf(w, "cpu.runtime_kernel_ns\t%d\t\n", stats.Processor.RuntimeKernelNS)
	}
	if stats.Memory != nil {
		fmt.Fprintf(w, "memory.commit_bytes\t%d\t\n", stats.Memory.MemoryUsageCommitBytes)
		fmt.Fprintf(w, "memory.commit_peak_bytes\t%d\t\n", stats.Memory.MemoryUsageCommitPeakBytes)
		fmt.Fprintf(w, "memory.private_working_set_bytes\t%d\t\n", stats.Memory.MemoryUsagePrivateWorkingSetBytes)
	}
	if stats.Storage != nil {
		fmt.Fprintf(w, "storage.read_count_normalized\t%d\t\n", stats.Storage.ReadCountNormalized)
		fmt.Fprintf(w, "storage.read_size_bytes\t%d\t\n", stats.Storage.ReadSizeBytes)
		fmt.Fprintf(w, "storage.write_count_normalized\t%d\t\n", stats.Storage.WriteCountNormalized)
		fmt.Fprintf(w, "storage.write_size_bytes\t%d\t\n", stats.Storage.WriteSizeBytes)
	}
}

func printWindowsVMStatistics(w *tabwriter.Writer, stats *wstats.VirtualMachineStatistics) {
	fmt.Fprintf(w, "METRIC\tVALUE\t\n")
	if stats.Processor != nil {
		fmt.Fprintf(w, "vm.cpu.total_runtime_ns\t%d\t\n", stats.Processor.TotalRuntimeNS)
	}
	if stats.Memory != nil {
		fmt.Fprintf(w, "vm.memory.working_set_bytes\t%d\t\n", stats.Memory.WorkingSetBytes)
		fmt.Fprintf(w, "vm.memory.virtual_node_count\t%d\t\n", stats.Memory.VirtualNodeCount)
		fmt.Fprintf(w, "vm.memory.available\t%d\t\n", stats.Memory.VmMemory.AvailableMemory)
		fmt.Fprintf(w, "vm.memory.available_buffer\t%d\t\n", stats.Memory.VmMemory.AvailableMemoryBuffer)
		fmt.Fprintf(w, "vm.memory.reserved\t%d\t\n", stats.Memory.VmMemory.ReservedMemory)
		fmt.Fprintf(w, "vm.memory.assigned\t%d\t\n", stats.Memory.VmMemory.AssignedMemory)
		fmt.Fprintf(w, "vm.memory.slp_active\t%t\t\n", stats.Memory.VmMemory.SlpActive)
		fmt.Fprintf(w, "vm.memory.balancing_enabled\t%t\t\n", stats.Memory.VmMemory.BalancingEnabled)
		fmt.Fprintf(w, "vm.memory.dm_operation_in_progress\t%t\t\n", stats.Memory.VmMemory.DmOperationInProgress)
	}
}

func allocMetricWstats(metric *types.Metric) interface{} {
	if typeurl.Is(metric.Data, (*wstats.Statistics)(nil)) {
		return &wstats.Statistics{}
	}
	return nil
}
