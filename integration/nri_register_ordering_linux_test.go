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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNRIRegisterBeforeCRIBackgroundServices verifies that the CRI service
// registers the CRI domain with NRI before it starts any background service.
//
// If NRI registration fails, criService.Run returns an error and containerd
// exits. When Register ran after the background services (the stats collector,
// event monitor, CNI conf syncers, and streaming server), those services -
// including the streaming server accepting connections on its port - were
// started during a startup that was already doomed to fail.
//
// This test forces NRI registration to fail deterministically by pointing the
// NRI socket at a path whose parent is a regular file, so creating the socket
// directory fails with ENOTDIR for any user. It then asserts that none of the
// "Start ..." background-service log lines were emitted before containerd
// exited. On the buggy ordering those lines appear; with registration moved
// ahead of the services they do not.
func TestNRIRegisterBeforeCRIBackgroundServices(t *testing.T) {
	workDir := t.TempDir()

	// A regular file cannot be a socket's parent directory: os.MkdirAll on it
	// returns ENOTDIR, so nri.Start (and therefore nri.Register) fails.
	notADir := filepath.Join(workDir, "nri-not-a-dir")
	require.NoError(t, os.WriteFile(notADir, nil, 0600))
	nriSocket := filepath.Join(notADir, "nri.sock")

	config := fmt.Sprintf(`
version = 3

[plugins.'io.containerd.nri.v1.nri']
  disable = false
  socket_path = '%s'
  plugin_registration_timeout = '5s'
  plugin_request_timeout = '2s'
`, nriSocket)

	configPath := filepath.Join(workDir, "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	// Launch containerd directly rather than through newCtrdProc: this process
	// is expected to exit non-zero because the failed NRI registration is fatal
	// for the CRI service, and newCtrdProc asserts a clean exit.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, *containerdBin,
		"--root", filepath.Join(workDir, "root"),
		"--state", filepath.Join(workDir, "state"),
		"--address", filepath.Join(workDir, "containerd.sock"),
		"--config", configPath,
		"--log-level", "info",
	)
	out, runErr := cmd.CombinedOutput()
	logs := string(out)

	// A timeout means containerd stayed up; the NRI failure must be fatal.
	require.NoError(t, ctx.Err(), "containerd did not exit; NRI registration failure should be fatal\n%s", logs)
	// The failed NRI registration must make containerd exit non-zero. Checked
	// after ctx.Err() so a timeout kill is not mistaken for the fatal exit.
	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr, "containerd should exit non-zero on the fatal NRI failure\n%s", logs)

	require.Contains(t, logs, "failed to set up NRI for CRI service",
		"expected containerd to fail CRI startup on NRI registration\n%s", logs)

	// Regression assertion: none of the background services may start once NRI
	// registration has failed, because Register runs before all of them.
	for _, unexpected := range []string{
		"Start stats collector",
		"Start event monitor",
		"Start streaming server",
	} {
		assert.NotContains(t, logs, unexpected,
			"%q must not be logged when NRI registration fails before the services start\n%s", unexpected, logs)
	}
}
