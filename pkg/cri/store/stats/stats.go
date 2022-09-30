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

package stats

// ContainerStats contains the information about container stats.
type ContainerStats struct {
	ContainerCPUStats
	ContainerMemoryStats
	ContainerFileSystemStats
}

type ContainerCPUStats struct {
	// Timestamp in nanoseconds at which the information were collected. Must be > 0.
	Timestamp int64
	// Cumulative CPU usage (sum across all cores) since object creation.
	UsageCoreNanoSeconds uint64
	// Total CPU usage (sum of all cores) averaged over the sample window.
	// The "core" unit can be interpreted as CPU core-nanoseconds per second.
	UsageNanoCores uint64
}

type ContainerMemoryStats struct {
	// Timestamp in nanoseconds at which the information were collected. Must be > 0.
	Timestamp int64
	// The amount of working set memory in bytes.
	WorkingSetBytes uint64
	// Available memory for use. This is defined as the memory limit - workingSetBytes.
	AvailableBytes uint64
	// Total memory in use. This includes all memory regardless of when it was accessed.
	UsageBytes uint64
	// The amount of anonymous and swap cache memory (includes transparent hugepages).
	RssBytes uint64
	// Cumulative number of minor page faults.
	PageFaults uint64
	// Cumulative number of major page faults.
	MajorPageFaults uint64
}

type ContainerFileSystemStats struct {
	// Timestamp in nanoseconds at which the information were collected. Must be > 0.
	Timestamp int64
	// The unique identifier of the filesystem.
	FsID FilesystemIdentifier
	// UsedBytes represents the bytes used for images on the filesystem.
	// This may differ from the total bytes used on the filesystem and may not
	// equal CapacityBytes - AvailableBytes.
	UsedBytes uint64
	// InodesUsed represents the inodes used by the images.
	// This may not equal InodesCapacity - InodesAvailable because the underlying
	// filesystem may also be used for purposes other than storing images.
	InodesUsed uint64
}

type FilesystemIdentifier struct {
	// Mountpoint of a filesystem.
	Mountpoint string
}
