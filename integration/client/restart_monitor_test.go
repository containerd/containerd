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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	. "github.com/containerd/containerd"
	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/pkg/testutil"
	"github.com/containerd/containerd/runtime/restart"
	srvconfig "github.com/containerd/containerd/services/server/config"
	"github.com/containerd/typeurl/v2"
	exec "golang.org/x/sys/execabs"
)

//nolint:unused // Ignore on non-Linux
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

	tempDir := t.TempDir()

	configTOMLFile := filepath.Join(tempDir, "config.toml")
	if err := os.WriteFile(configTOMLFile, []byte(configTOML), 0600); err != nil {
		t.Fatal(err)
	}

	if err := srvconfig.LoadConfig(configTOMLFile, &configTOMLDecoded); err != nil {
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
	if err := ctrd.start("containerd", address, args, buf, buf); err != nil {
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
		if err := forceRemoveAll(tempDir); err != nil {
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
	const (
		interval = 5 * time.Second
	)

	configTOML := fmt.Sprintf(`
version = 2
[plugins]
  [plugins."io.containerd.internal.v1.restart"]
	  interval = "%s"
`, interval.String())
	client, _, cleanup := newDaemonWithConfig(t, configTOML)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()

	_, err := client.Pull(ctx, testImage, WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Always", func(t *testing.T) {
		testRestartMonitorAlways(t, client, interval)
	})
	t.Run("Failure Policy", func(t *testing.T) {
		testRestartMonitorWithOnFailurePolicy(t, client, interval)
	})
}

// testRestartMonitorAlways restarts its container always.
func testRestartMonitorAlways(t *testing.T, client *Client, interval time.Duration) {
	const (
		epsilon = 1 * time.Second
		count   = 20
	)

	var (
		ctx, cancel = testContext(t)
		id          = strings.ReplaceAll(t.Name(), "/", "_")
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(
			oci.WithImageConfig(image),
			longCommand,
		),
		restart.WithStatus(Running),
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

	statusC, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	// Wait for task exit. If the task takes longer to exit, we risc
	// wrongfully determining that the task has been restarted when we
	// check the status in the for loop bellow and find that it's still
	// running.
	select {
	case <-statusC:
	case <-time.After(30 * time.Second):
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

// testRestartMonitorWithOnFailurePolicy restarts its container with `on-failure:1`
func testRestartMonitorWithOnFailurePolicy(t *testing.T, client *Client, interval time.Duration) {
	var (
		ctx, cancel = testContext(t)
		id          = strings.ReplaceAll(t.Name(), "/", "_")
	)
	defer cancel()

	image, err := client.GetImage(ctx, testImage)
	if err != nil {
		t.Fatal(err)
	}

	policy, _ := restart.NewPolicy("on-failure:1")
	container, err := client.NewContainer(ctx, id,
		WithNewSnapshot(id, image),
		WithNewSpec(
			oci.WithImageConfig(image),
			// always exited with 1
			withExitStatus(1),
		),
		restart.WithStatus(Running),
		restart.WithPolicy(policy),
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

	statusCh, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	eventCh, eventErrCh := client.Subscribe(ctx, `topic=="/tasks/create"`)

	select {
	case <-statusCh:
	case <-time.After(30 * time.Second):
		t.Fatal("should receive exit event in time")
	}

	select {
	case e := <-eventCh:
		cid, err := convertTaskCreateEvent(e.Event)
		if err != nil {
			t.Fatal(err)
		}
		if cid != id {
			t.Fatalf("expected task id = %s, but got %s", id, cid)
		}
	case err := <-eventErrCh:
		t.Fatalf("unexpected error from event channel: %v", err)
	case <-time.After(1 * time.Minute):
		t.Fatal("should receive create event in time")
	}

	labels, err := container.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restartCount, _ := strconv.Atoi(labels[restart.CountLabel])
	if restartCount != 1 {
		t.Fatalf("expected restart count to be 1, got %d", restartCount)
	}
}

func convertTaskCreateEvent(e typeurl.Any) (string, error) {
	id := ""

	evt, err := typeurl.UnmarshalAny(e)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshalany: %w", err)
	}

	switch e := evt.(type) {
	case *eventtypes.TaskCreate:
		id = e.ContainerID
	default:
		return "", errors.New("unsupported event")
	}
	return id, nil
}
