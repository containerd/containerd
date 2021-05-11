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

package integration

import (
	"context"
	"testing"

	"github.com/containerd/cgroups"
	"github.com/containerd/containerd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkActualMemoryLimit(t *testing.T, container containerd.Container, expectedBytes uint64) {
	t.Log("Check memory limit in cgroup")
	task, err := container.Task(context.Background(), nil)
	require.NoError(t, err)
	cgroup, err := cgroups.Load(cgroups.V1, cgroups.PidPath(int(task.Pid())))
	require.NoError(t, err)
	stat, err := cgroup.Stat(cgroups.IgnoreNotExist)
	require.NoError(t, err)
	assert.Equal(t, expectedBytes, stat.Memory.Usage.Limit)
}
