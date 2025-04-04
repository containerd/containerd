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

import (
	"fmt"
	"testing"
	"time"

	"github.com/containerd/go-cni"
	"github.com/stretchr/testify/assert"

	servertesting "github.com/containerd/containerd/v2/internal/cri/testing"
)

func TestCNINetPluginLifecycle(t *testing.T) {
	cniNetPlugin := newCNINetPlugin()
	assert.NotNil(t, cniNetPlugin)

	count := 5
	for i := range count {
		err := cniNetPlugin.add(fmt.Sprintf("cni-%d", i), t.TempDir(), servertesting.NewFakeCNIPlugin(), []cni.Opt{})
		assert.NoError(t, err)
	}

	errCh := cniNetPlugin.start()

	newConfDir := t.TempDir()
	err := cniNetPlugin.add("cni-0", newConfDir, servertesting.NewFakeCNIPlugin(), []cni.Opt{})
	assert.NoError(t, err)
	assert.Equal(t, count, len(cniNetPlugin.plugins))
	assert.Equal(t, count, len(cniNetPlugin.confMonitor))
	assert.Equal(t, newConfDir, cniNetPlugin.confMonitor["cni-0"].confDir)

	assert.NoError(t, cniNetPlugin.close())
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cniNetPlugin to close syncers")
	}
}
