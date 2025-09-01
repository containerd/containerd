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

const (
	SpanCRICreateContainer = "cri.create_container"
	SpanCRIStartContainer  = "cri.start_container"
	SpanCRIStopContainer   = "cri.stop_container"
	SpanCRIRemoveContainer = "cri.remove_container"
	SpanCRIPullImage       = "cri.pull_image"
	SpanCRICreateSandbox   = "cri.create_sandbox"
	SpanCRIStartSandbox    = "cri.start_sandbox"
	SpanCRIStopSandbox     = "cri.stop_sandbox"
	SpanCRIRemoveSandbox   = "cri.remove_sandbox"
	SpanCNISetup           = "cni.setup_pod_network"
	SpanCNITeardown        = "cni.teardown_pod_network"
	SpanUsernsCreate       = "userns.create_mapping"
	SpanSnapshotPrepare    = "snapshot.prepare"
	SpanSnapshotCommit     = "snapshot.commit"
	SpanMountSnapshot      = "mount.snapshot"
	SpanUnmountSnapshot    = "mount.unmount_snapshot"
	SpanRuntimeTaskCreate  = "runtime.task_create"
	SpanRuntimeTaskStart   = "runtime.task_start"
	SpanRuntimeTaskKill    = "runtime.task_kill"
)
