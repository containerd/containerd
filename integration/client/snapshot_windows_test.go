//go:build windows
// +build windows

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
package client

import (
	"testing"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/snapshots/testsuite"
)

func runTestSnapshotterClient(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	// The SeBackupPrivilege and SeRestorePrivilege gives us access to system files inside the container mount points
	// (and everywhere on the system), without having to explicitly set DACLs on each location inside the mount point.
	if err := winio.EnableProcessPrivileges([]string{winio.SeBackupPrivilege, winio.SeRestorePrivilege}); err != nil {
		t.Error(err)
	}
	defer winio.DisableProcessPrivileges([]string{winio.SeBackupPrivilege, winio.SeRestorePrivilege})
	testsuite.SnapshotterSuite(t, "SnapshotterClient", newSnapshotter)
}
