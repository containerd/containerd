//go:build linux

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

package oom

type EventFunc func(containerID string)

type Interface interface {
	// Add is to start to monitor container cgroupv2 OOM event.
	//
	// TODO:
	//
	// Currently, cgroupsv2 package doesn't support to export cgroupv2 path.
	// Ideally, the function interface should be like
	//
	// Add(string, *cgroupsv2.Manager, EventFunc) error
	Add(containerID string, pid int, fn EventFunc) error
	//
	// Stop is to stop monitor OOM event for a given container ID
	Stop(containerID string) error
}
