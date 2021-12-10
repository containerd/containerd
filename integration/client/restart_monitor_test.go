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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	. "github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/testutil"
	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/containerd/containerd/sys"
	exec "golang.org/x/sys/execabs"
)

// the following nolint is for shutting up gometalinter on non-linux.
// nolint: unused
func newDaemonWithConfig(t *testing.T, configTOML string) (*Client, *daemon, func()) {
	if testing.Short() {
		t.Skip()
	}
	testutil.RequiresRoot(t)
	var (
		ctrd              = daemon{}
		configTOMLDecoded srvconfig.Config
		buf               = bytes.NewBuffer(nil)
	)

	tempDir, err := os.MkdirTemp("", "containerd-test-new-daemon-with-config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tempDir)
		}
	}()

	configTOMLFile := filepath.Join(tempDir, "config.toml")
	if err = os.WriteFile(configTOMLFile, []byte(configTOML), 0600); err != nil {
		t.Fatal(err)
	}

	if err = srvconfig.LoadConfig(configTOMLFile, &configTOMLDecoded); err != nil {
		t.Fatal(err)
	}

	address := configTOMLDecoded.GRPC.Address
	if address == "" {
		if runtime.GOOS == "windows" {
			address = fmt.Sprintf(`\\.\pipe\containerd-containerd-test-%s`, filepath.Base(tempDir))
		} else {
			address = filepath.Join(tempDir, "containerd.sock")
		}
	}
	args := []string{"-c", configTOMLFile}
	if configTOMLDecoded.Root == "" {
		args = append(args, "--root", filepath.Join(tempDir, "root"))
	}
	if configTOMLDecoded.State == "" {
		args = append(args, "--state", filepath.Join(tempDir, "state"))
	}
	if err = ctrd.start("containerd", address, args, buf, buf); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}

	waitCtx, waitCancel := context.WithTimeout(context.TODO(), 2*time.Second)
	client, err := ctrd.waitForStart(waitCtx)
	waitCancel()
	if err != nil {
		ctrd.Kill()
		ctrd.Wait()
		t.Fatalf("%v: %s", err, buf.String())
	}

	cleanup := func() {
		if err := client.Close(); err != nil {
			t.Errorf("failed to close client: %v", err)
		}
		if err := ctrd.Stop(); err != nil {
			if err := ctrd.Kill(); err != nil {
				t.Errorf("failed to signal containerd: %v", err)
			}
		}
		if err := ctrd.Wait(); err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				t.Errorf("failed to wait for: %v", err)
			}
		}
		if err := sys.ForceRemoveAll(tempDir); err != nil {
			t.Errorf("failed to remove %s: %v", tempDir, err)
		}
		if t.Failed() {
			t.Log("Daemon output:\n", buf.String())
		}

		// cleaning config-specific resources is up to the caller
	}
	return client, &ctrd, cleanup
}

// TestRestartMonitor tests restarting containers
// with the restart monitor service plugin
func TestRestartMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		// This test on Windows encounters the following error in some environments:
		// "The process cannot access the file because it is being used by another process. (0x20)"
		// Skip this test until this error can be evaluated and the appropriate
		// test fix or environment configuration can be determined.
		t.Skip("Skipping flaky test on Windows")
	}
	const (
		interval = 10 * time.Second
		epsilon  = 1 * time.Second
		count    = 20
	)
	configTOML := fmt.Sprintf(`
version = 2
[plugins]
  [plugins."io.containerd.internal.v1.restart"]
	  interval = "%s"
`, interval.String())
	client, _, cleanup := newDaemonWithConfig(t, configTOML)
	defer cleanup()

	var (
		ctx, cancel = testContext(t)
		id          = t.Name()
	)
	defer cancel()

	image, err := client.Pull(ctx, testImage, WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(
			oci.WithImageConfig(image),
			longCommand,
		),
		withRestartStatus(Running),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := container.Delete(ctx, WithSnapshotCleanup); err != nil {
			t.Logf("failed to delete container: %v", err)
		}
	}()

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := task.Delete(ctx, WithProcessKill); err != nil {
			t.Logf("failed to delete task: %v", err)
		}
	}()

	if err := task.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	begin := time.Now()
	lastCheck := begin

	expected := begin.Add(interval).Add(epsilon)

	// Deadline determines when check for restart should be aborted.
	deadline := begin.Add(interval).Add(epsilon * count)
	for {
		status, err := task.Status(ctx)
		now := time.Now()
		if err != nil {
			// ErrNotFound is expected here, because the restart monitor
			// temporarily removes the task before restarting.
			t.Logf("%v: err=%v", now, err)
		} else {
			t.Logf("%v: status=%q", now, status.Status)

			if status.Status == Running {
				break
			}
		}

		// lastCheck represents the last time the status was seen as not running
		lastCheck = now
		if lastCheck.After(deadline) {
			t.Logf("%v: the task was not restarted", lastCheck)
			return
		}
		time.Sleep(epsilon)
	}

	// Use the last timestamp for when the process was seen as not running for the check
	if lastCheck.After(expected) {
		t.Fatalf("%v: the task was restarted, but it must be before %v", lastCheck, expected)
	}
	t.Logf("%v: the task was restarted since %v", time.Now(), lastCheck)
}

// withRestartStatus is a copy of "github.com/containerd/containerd/runtime/restart".WithStatus.
// This copy is needed because `go test` refuses circular imports.
func withRestartStatus(status ProcessStatus) func(context.Context, *Client, *containers.Container) error {
	return func(_ context.Context, _ *Client, c *containers.Container) error {
		if c.Labels == nil {
			c.Labels = make(map[string]string)
		}
		c.Labels["containerd.io/restart.status"] = string(status)
		return nil
	}
}
