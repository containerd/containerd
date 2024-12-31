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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// test issue https://github.com/containerd/containerd/issues/11209
func TestCniDirRename(t *testing.T) {
	err := os.Rename("/etc/cni/net.d", "/etc/cni/net.d1")
	require.NoError(t, err)
	time.Sleep(time.Second * 3)
	status, err := runtimeService.Status()
	require.NoError(t, err)
	info := status.GetInfo()
	require.Equal(t, true, strings.Contains(info["lastCNILoadStatus"], "cni config load failed"))
	err = os.Rename("/etc/cni/net.d1", "/etc/cni/net.d")
	require.NoError(t, err)
	time.Sleep(time.Second * 6)
	status, err = runtimeService.Status()
	require.NoError(t, err)
	info = status.GetInfo()
	require.Equal(t, "OK", info["lastCNILoadStatus"])
}
