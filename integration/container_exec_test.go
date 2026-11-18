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
	"bytes"
	"context"
	"io"
	"net/url"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/integration/images"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	remoteclient "k8s.io/client-go/tools/remotecommand"
	criruntime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestContainerDrainExecIOAfterExit(t *testing.T) {
	if runtimeHandlerIsRunsc() {
		t.Skip("runsc does not support draining exec IO for background processes")
	}
	// FIXME(fuweid): support it for windows container.
	if runtime.GOOS == "windows" {
		t.Skip("it seems that windows platform doesn't support detached process. skip it")
	}

	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-exec-drain-io-after-exit")

	var (
		testImage     = images.Get(images.BusyBox)
		containerName = "test-container-exec"
	)

	EnsureImageExists(t, testImage)

	t.Log("Create a container")
	cnConfig := ContainerConfig(
		containerName,
		testImage,
		WithCommand("sh", "-c", "sleep 365d"),
	)

	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	}()

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	defer func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	}()

	t.Log("Exec in container")
	_, _, err = runtimeService.ExecSync(cn, []string{"sh", "-c", "sleep 365d &"}, 5*time.Second)
	require.ErrorContains(t, err, "failed to drain exec process")

	t.Log("Exec in container")
	_, _, err = runtimeService.ExecSync(cn, []string{"sh", "-c", "sleep 2s &"}, 10*time.Second)
	require.NoError(t, err, "should drain IO in time")
}

// TestContainerExecStdinNotTruncatedWhenStdoutClosed is the regression test for
// containerd#12734: stdin must not be truncated when the exec process closes
// its stdout while it is still reading stdin.
func TestContainerExecStdinNotTruncatedWhenStdoutClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test requires sh and the CRI exec stdio on Linux")
	}

	t.Log("Create a sandbox")
	sb, sbConfig := PodSandboxConfigWithCleanup(t, "sandbox", "container-exec-stdin-stdout-closed")

	testImage := images.Get(images.BusyBox)
	EnsureImageExists(t, testImage)

	t.Log("Create a container")
	cnConfig := ContainerConfig(
		"test-container-exec-stdin",
		testImage,
		WithCommand("sh", "-c", "sleep 365d"),
	)
	cn, err := runtimeService.CreateContainer(sb, cnConfig, sbConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.RemoveContainer(cn))
	})

	t.Log("Start the container")
	require.NoError(t, runtimeService.StartContainer(cn))
	t.Cleanup(func() {
		assert.NoError(t, runtimeService.StopContainer(cn, 10))
	})

	t.Log("Exec in container")
	// The exec process closes its stdout and then keeps reading stdin, which is
	// the reproducer of containerd#12734.
	rsp, err := runtimeService.Exec(&criruntime.ExecRequest{
		ContainerId: cn,
		Cmd:         []string{"sh", "-c", "exec 1>&-; dd of=/tmp/stdin-data bs=4K"},
		Stdin:       true,
		Stdout:      true,
		Stderr:      true,
	})
	require.NoError(t, err)

	execURL, err := url.Parse(rsp.Url)
	require.NoError(t, err)
	executor, err := remoteclient.NewSPDYExecutor(&rest.Config{}, "POST", execURL)
	require.NoError(t, err)

	// The stdin is sent only after the exec process closed its stdout, so that
	// the truncation is triggered deterministically.
	payload := bytes.Repeat([]byte("containerd-exec-stdin\n"), 128*1024)
	require.NoError(t, executor.StreamWithContext(context.Background(), remoteclient.StreamOptions{
		Stdin:  &delayedStdinReader{data: payload, delay: 2 * time.Second},
		Stdout: io.Discard,
		Stderr: io.Discard,
	}))

	stdout, stderr, err := runtimeService.ExecSync(cn, []string{"cat", "/tmp/stdin-data"}, 0)
	require.NoError(t, err, "stderr: %s", string(stderr))
	require.Equal(t, len(payload), len(stdout), "stdin is truncated: expected %d bytes, got %d", len(payload), len(stdout))
	require.True(t, bytes.Equal(payload, stdout), "stdin content mismatch")
}

// delayedStdinReader returns data after delay, to make sure that the exec
// process has closed its stdout before the client writes stdin.
type delayedStdinReader struct {
	data  []byte
	delay time.Duration
	once  sync.Once
}

func (r *delayedStdinReader) Read(p []byte) (int, error) {
	r.once.Do(func() { time.Sleep(r.delay) })
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
