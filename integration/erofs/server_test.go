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

// server_test.go configures and starts the in-process containerd server used
// by the EROFS integration suite. It imports exactly the plugins needed for
// EROFS testing; unused plugins (overlayfs, CRI, NRI, ttrpc, metrics, …) are
// simply not imported rather than disabled via config.
package erofs

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	ctrdsrv "github.com/containerd/containerd/v2/cmd/containerd/server"
	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log"

	// Runtime (needed for task service).
	_ "github.com/containerd/containerd/v2/core/runtime/v2"

	// Content, metadata, GC, leases.
	_ "github.com/containerd/containerd/v2/plugins/content/local/plugin"
	_ "github.com/containerd/containerd/v2/plugins/events"
	_ "github.com/containerd/containerd/v2/plugins/gc"
	_ "github.com/containerd/containerd/v2/plugins/leases"
	_ "github.com/containerd/containerd/v2/plugins/metadata"

	// Differ plugins: EROFS differ + walking fallback.
	_ "github.com/containerd/containerd/v2/plugins/diff/erofs/plugin"
	_ "github.com/containerd/containerd/v2/plugins/diff/walking/plugin"

	// fsview EROFS handler so fsview.FSMounts works for erofs mounts.
	_ "github.com/containerd/containerd/v2/plugins/mount/fsview/erofs"

	// Image verifier, mount manager, sandbox controller.
	_ "github.com/containerd/containerd/v2/plugins/imageverifier"
	_ "github.com/containerd/containerd/v2/plugins/mount"
	_ "github.com/containerd/containerd/v2/plugins/sandbox"

	// GRPC server (unix socket / named pipe).
	_ "github.com/containerd/containerd/v2/plugins/server/grpc"

	// GRPC service plugins.
	_ "github.com/containerd/containerd/v2/plugins/services/containers"
	_ "github.com/containerd/containerd/v2/plugins/services/content"
	_ "github.com/containerd/containerd/v2/plugins/services/diff"
	_ "github.com/containerd/containerd/v2/plugins/services/events"
	_ "github.com/containerd/containerd/v2/plugins/services/healthcheck"
	_ "github.com/containerd/containerd/v2/plugins/services/images"
	_ "github.com/containerd/containerd/v2/plugins/services/introspection"
	_ "github.com/containerd/containerd/v2/plugins/services/leases"
	_ "github.com/containerd/containerd/v2/plugins/services/namespaces"
	_ "github.com/containerd/containerd/v2/plugins/services/sandbox"
	_ "github.com/containerd/containerd/v2/plugins/services/snapshots"
	_ "github.com/containerd/containerd/v2/plugins/services/tasks"
	_ "github.com/containerd/containerd/v2/plugins/services/version"

	// EROFS snapshotter.
	_ "github.com/containerd/containerd/v2/plugins/snapshots/erofs/plugin"

	// Streaming and transfer service.
	_ "github.com/containerd/containerd/v2/plugins/streaming"
	_ "github.com/containerd/containerd/v2/plugins/transfer"
)

// startInProcessServer creates and starts a containerd server with all plugins
// running inside the current process. It returns when the GRPC server is
// listening on sock.
func startInProcessServer(ctx context.Context, tmpDir, sock string) (*ctrdsrv.Server, error) {
	cfg := serverConfig(tmpDir, sock)

	srv, err := ctrdsrv.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("server.New: %w", err)
	}
	if err := srv.Start(ctx); err != nil {
		return nil, fmt.Errorf("server.Start: %w", err)
	}
	return srv, nil
}

// serverConfig returns the containerd configuration for the in-process test
// server:
//   - GRPC address set to the per-run temp socket.
//   - Differ order ["erofs", "walking"] so the EROFS differ is tried first.
//   - Transfer plugin with empty unpack_config so tests call Unpack explicitly.
func serverConfig(tmpDir, sock string) *srvconfig.Config {
	return &srvconfig.Config{
		Version: 2,
		Root:    filepath.Join(tmpDir, "root"),
		State:   filepath.Join(tmpDir, "state"),
		Plugins: map[string]any{
			// Route GRPC to the per-run temp socket.
			"io.containerd.server.v1.grpc": map[string]any{
				"address": sock,
			},
			// Prefer the EROFS differ; fall back to walking for non-EROFS
			// images (e.g. exec_linux_test.go when running as root).
			"io.containerd.service.v1.diff-service": map[string]any{
				"default": []string{"erofs", "walking"},
			},
			// Empty unpack_config so the transfer service does not try to
			// resolve snapshotters automatically; tests call Unpack explicitly.
			"io.containerd.transfer.v1.local": map[string]any{
				"unpack_config": []any{},
			},
		},
	}
}

// waitAndConfigureServer polls sock until the server is healthy, then sets the
// namespace default snapshotter label for the test namespace.
func waitAndConfigureServer(ctx context.Context, sock string) error {
	deadline := time.Now().Add(30 * time.Second)
	var client *containerd.Client
	for time.Now().Before(deadline) {
		// Check the socket is reachable at the network layer before attempting
		// the gRPC health check.
		conn, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		conn.Close()

		// Socket reachable — try the gRPC health check with a short timeout.
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		c, err := containerd.New(sock)
		if err != nil {
			cancel()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		ok, err := c.IsServing(attemptCtx)
		cancel()
		if err != nil || !ok {
			c.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		client = c
		break
	}
	if client == nil {
		return fmt.Errorf("timed out waiting for in-process containerd on %s", sock)
	}
	defer client.Close()

	v, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("version check: %w", err)
	}
	log.G(ctx).WithFields(log.Fields{
		"version":  v.Version,
		"revision": v.Revision,
	}).Info("EROFS integration suite: in-process containerd ready")

	// Set the default snapshotter for the test namespace.
	nsCtx := namespaces.WithNamespace(ctx, testNamespace)
	if err := client.NamespaceService().SetLabel(nsCtx, testNamespace,
		defaults.DefaultSnapshotterNSLabel, defaults.DefaultSnapshotter); err != nil {
		log.G(ctx).WithError(err).Debug("could not set default snapshotter label")
	}
	return nil
}
