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

package server

// cgroupDelegateAnnotations returns systemd Delegate annotations for writable cgroup
// containers on cgroup v2 when the workload is not privileged.
func cgroupDelegateAnnotations(cgroupWritable, unifiedCgroups, privileged bool) map[string]string {
	if !cgroupWritable || !unifiedCgroups || privileged {
		return nil
	}
	return map[string]string{
		"org.systemd.property.Delegate": "true",
	}
}
