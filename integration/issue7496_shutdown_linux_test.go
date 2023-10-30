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

	"github.com/stretchr/testify/require"

	apitask "github.com/containerd/containerd/v2/api/runtime/task/v3"
	"github.com/containerd/containerd/v2/namespaces"
)

// TestIssue7496_ShouldRetryShutdown is based on https://github.com/containerd/containerd/issues/7496.
//
// Assume that the shim.Delete takes almost 10 seconds and returns successfully
// and there is no container in shim. However, the context is close to be
// canceled. It will fail to call Shutdown. If we ignores the Canceled error,
// the shim will be leaked. In order to reproduce this, this case will use
// failpoint to inject error into Shutdown API, and then check whether the shim
// is leaked.
func TestIssue7496_ShouldRetryShutdown(t *testing.T) {
	// TODO: re-enable if we can retry Shutdown API.
	t.Skipf("Please re-enable me if we can retry Shutdown API")

	ctx := namespaces.WithNamespace(context.Background(), "k8s.io")

	t.Logf("Create a pod config with shutdown failpoint")
	sbConfig := PodSandboxConfig("sandbox", "issue7496_shouldretryshutdown")
	injectShimFailpoint(t, sbConfig, map[string]string{
		"Shutdown": "1*error(please retry)",
	})

	t.Logf("RunPodSandbox")
	sbID, err := runtimeService.RunPodSandbox(sbConfig, failpointRuntimeHandler)
	require.NoError(t, err)

	t.Logf("Connect to the shim %s", sbID)
	shimCli := connectToShim(ctx, t, sbID)

	t.Logf("Log shim %s's pid: %d", sbID, shimPid(ctx, t, shimCli))

	t.Logf("StopPodSandbox and RemovePodSandbox")
	require.NoError(t, runtimeService.StopPodSandbox(sbID))
	require.NoError(t, runtimeService.RemovePodSandbox(sbID))

	t.Logf("Check the shim connection")
	_, err = shimCli.Connect(ctx, &apitask.ConnectRequest{})
	require.Error(t, err, "should failed to call shim connect API")
}
