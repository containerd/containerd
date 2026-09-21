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

package sandbox

import (
	"context"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/core/events/exchange"
	v2 "github.com/containerd/containerd/v2/core/runtime/v2"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func TestStatusWithoutShim(t *testing.T) {
	// A sandbox whose shim is gone, or that this controller never started,
	// is reported as not found. The callers (CRI recovery, PodSandboxStatus)
	// map that to not ready.
	shims, err := v2.NewShimManager(&v2.ManagerConfig{Events: exchange.NewExchange()})
	require.NoError(t, err)
	c := &controllerLocal{shims: shims}

	_, err = c.Status(namespaces.WithNamespace(context.Background(), "test"), "no-such-sandbox", false)
	require.Error(t, err)
	assert.True(t, errdefs.IsNotFound(err), "got %v", err)
}
