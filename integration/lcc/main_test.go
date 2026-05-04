//go:build linux && integration

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

// Package lcc contains end-to-end integration tests for the overlay
// snapshotter's layer content caching feature.
//
// Run as root with:
//
//	go test -tags integration -v ./integration/lcc/
package lcc

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/klauspost/compress/zstd"
)

// testEnv holds the running test stack: containerd subprocess and a Go client
// connected to it.
type testEnv struct {
	ctx        context.Context
	tmp        string
	ctrdRoot   string
	cacheDir   string
	ctrdSock   string
	ctrdClient *client.Client
}

// setupEnv starts the integration stack and registers t.Cleanup handlers.
// Each call gets its own isolated tmp directory, registry port, and containerd
// root, so multiple tests can run without interfering with each other.
func setupEnv(t *testing.T) *testEnv {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)

	tmp := t.TempDir()
	ctrdRoot := filepath.Join(tmp, "containerd")

	// Use a fixed short socket path to stay within the 108-byte Unix socket
	// limit. Tests are serialised so there is no conflict.
	ctrdSock := fmt.Sprintf("/run/lcc-test-%d.sock", os.Getpid())

	// overlayfs requires upperdir/workdir to live on a non-overlayfs
	// filesystem. Inside a privileged Docker container /tmp is on Docker's
	// overlayfs; mount a tmpfs on ctrdRoot so the overlay snapshotter's
	// snapshots/<id>/fs and cache/ directories land on a real filesystem.
	if err := os.MkdirAll(ctrdRoot, 0755); err != nil {
		t.Fatalf("mkdir ctrdRoot: %v", err)
	}
	if err := syscall.Mount("tmpfs", ctrdRoot, "tmpfs", 0, ""); err == nil {
		t.Cleanup(func() { _ = syscall.Unmount(ctrdRoot, syscall.MNT_DETACH) })
	} else if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("mount tmpfs on ctrdRoot: %v", err)
	}
	// EPERM means /tmp is already on a real FS — safe to continue without tmpfs.

	writeContainerdConfig(t, tmp)
	startContainerd(t, ctx, tmp, ctrdRoot, ctrdSock)
	t.Logf("containerd listening on %s", ctrdSock)

	ctrdClient, err := client.New(ctrdSock,
		client.WithDefaultNamespace("default"),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	t.Cleanup(func() { ctrdClient.Close() })

	const overlayPlugin = "io.containerd.snapshotter.v1.overlayfs"
	return &testEnv{
		ctx:        ctx,
		tmp:        tmp,
		ctrdRoot:   ctrdRoot,
		cacheDir:   filepath.Join(ctrdRoot, overlayPlugin, "cache"),
		ctrdSock:   ctrdSock,
		ctrdClient: ctrdClient,
	}
}

// checkBinaries fails the test if any of the named binaries are not in PATH.
func checkBinaries(t *testing.T, bins ...string) {
	t.Helper()
	for _, bin := range bins {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s not in PATH", bin)
		}
	}
}

// ctrImport imports an OCI image layout tarball into containerd's image store
// and unpacks it so that overlay snapshots exist before the test inspects them.
func ctrImport(t *testing.T, env *testEnv, tarPath string) {
	t.Helper()
	cmd := exec.CommandContext(env.ctx, "ctr",
		"--address", env.ctrdSock,
		"--namespace", "default",
		"images", "import",
		"--snapshotter", "overlayfs",
		tarPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ctr images import %s: %v\n%s", tarPath, err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	t.Logf("ctr import %s: %s", filepath.Base(tarPath), lines[len(lines)-1])
}

// ctrRun starts a container from ref using the overlay snapshotter, waits for
// it to exit, removes it (--rm), and returns its stdout.
func ctrRun(t *testing.T, env *testEnv, ref, containerID string, containerCmd ...string) string {
	t.Helper()
	args := []string{
		"--address", env.ctrdSock,
		"--namespace", "default",
		"run",
		"--rm",
		"--snapshotter", "overlayfs",
		ref,
		containerID,
	}
	args = append(args, containerCmd...)
	return runCmdCapture(t, env.ctx, "ctr", args...)
}

const (
	pruneMaxAttempts = 10
	pruneInterval    = 2 * time.Second
)

// pruneAll removes the image ref and polls until containerd's GC has evicted
// all snapshots and LCC cache directories. The GC calls Cleanup() on the
// overlay snapshotter, which removes LCC cache dirs no longer referenced by any
// remaining snapshot.
func pruneAll(t *testing.T, env *testEnv, ref string) {
	t.Helper()

	runCmd(t, env.ctx, "ctr",
		"--address", env.ctrdSock,
		"--namespace", "default",
		"images", "rm", ref,
	)

	snapSvc := env.ctrdClient.SnapshotService("overlayfs")

	var blobCount int
	var remaining int
	for attempt := range pruneMaxAttempts {
		time.Sleep(pruneInterval)

		blobCount = countCachedBlobs(t, env.cacheDir)
		remaining = 0
		_ = snapSvc.Walk(env.ctx, func(_ context.Context, _ snapshots.Info) error {
			remaining++
			return nil
		})
		if blobCount == 0 && remaining == 0 {
			break
		}
		t.Logf("pruneAll attempt %d/%d: %d blobs, %d snapshots remaining",
			attempt+1, pruneMaxAttempts, blobCount, remaining)
	}

	if blobCount != 0 {
		t.Errorf("want empty LCC cache dir after prune, got %d blobs", blobCount)
	}
	if remaining != 0 {
		t.Errorf("want 0 snapshots after prune, got %d", remaining)
	}
	t.Log("prune: LCC cache dir and snapshots empty")
}

// buildStaticBinary compiles a tiny CGO_ENABLED=0 Go binary that prints msg
// when executed. Returns the path to the compiled binary.
func buildStaticBinary(t *testing.T, tmp, name, msg string) string {
	t.Helper()
	srcDir := filepath.Join(tmp, "src-"+name)
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("MkdirAll %s: %v", srcDir, err)
	}
	src := fmt.Sprintf("package main\nimport \"fmt\"\nfunc main() { fmt.Print(%q) }\n", msg)
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module testbin\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	outBin := filepath.Join(tmp, name)
	cmd := exec.Command("go", "build", "-ldflags=-extldflags=-static", "-o", outBin, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build %s: %v", name, err)
	}
	return outBin
}

// makePrintBlob builds a statically-linked binary that prints msg, packs it
// into a tar+zstd archive at tarPath inside the archive, and returns the path
// to the archive.
func makePrintBlob(t *testing.T, dir, outFile, tarPath, msg string) string {
	t.Helper()
	binPath := buildStaticBinary(t, dir, outFile+"-bin", msg)
	binData, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", binPath, err)
	}

	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     tarPath,
		Size:     int64(len(binData)),
		Mode:     0755,
	}); err != nil {
		t.Fatalf("tar.WriteHeader: %v", err)
	}
	if _, err := tw.Write(binData); err != nil {
		t.Fatalf("tar.Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd.Close: %v", err)
	}
	path := filepath.Join(dir, outFile)
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile blob: %v", err)
	}
	return path
}

// writeContainerdConfig writes a minimal containerd config.toml for testing.
// Only the overlay snapshotter's LCC option is set here; root, state, address,
// and disabled plugins are passed as CLI flags by startContainerd.
func writeContainerdConfig(t *testing.T, tmp string) {
	t.Helper()
	const cfg = `version = 3
disabled_plugins = ["io.containerd.grpc.v1.cri"]

[plugins."io.containerd.snapshotter.v1.overlayfs"]
  layer_content_cache = true
`
	if err := os.WriteFile(filepath.Join(tmp, "containerd.toml"), []byte(cfg), 0644); err != nil {
		t.Fatalf("WriteFile containerd config: %v", err)
	}
}

// startContainerd starts containerd with the config written by
// writeContainerdConfig and waits for the gRPC socket to appear.
func startContainerd(t *testing.T, ctx context.Context, tmp, ctrdRoot, ctrdSock string) {
	t.Helper()
	// Remove any stale socket left by a previous unclean exit before binding.
	if err := os.Remove(ctrdSock); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale socket %s: %v", ctrdSock, err)
	}
	cmd := exec.CommandContext(ctx, "containerd",
		"--config", filepath.Join(tmp, "containerd.toml"),
		"--root", ctrdRoot,
		"--state", filepath.Join(tmp, "containerd-state"),
		"--address", ctrdSock,
		"--log-level", "warn",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start containerd: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForSocket(t, ctrdSock, 30*time.Second)
}

// runCmd runs a subprocess, routing its output to the test log.
// Fails the test on non-zero exit.
func runCmd(t *testing.T, ctx context.Context, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	t.Logf("+ %s %v", name, args)
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// runCmdCapture runs a subprocess and returns its stdout as a string.
func runCmdCapture(t *testing.T, ctx context.Context, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	t.Logf("+ %s %v", name, args)
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return stdout.String()
}

// waitForSocket polls until the Unix socket at path accepts connections.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
			conn.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for Unix socket %s", path)
}

// blobDirInodes returns a map from LCC cache directory path to its inode
// number. The layout is cache/<algorithm>.<hex>.<uid>.<gid>.<seq>/; this reads the flat cache dir.
// ENOENT returns an empty map.
//
// Used by TestLCCLayerCacheIndependence to distinguish cache hits (same inode)
// from registry re-fetches (directory recreated, new inode).
func blobDirInodes(t *testing.T, cacheDir string) map[string]uint64 {
	t.Helper()
	result := make(map[string]uint64)
	entries, err := os.ReadDir(cacheDir)
	if os.IsNotExist(err) {
		return result
	}
	if err != nil {
		t.Fatalf("reading cache dir %s: %v", cacheDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(cacheDir, e.Name())
		fi, err := os.Stat(full)
		if err != nil {
			t.Fatalf("stat %s: %v", full, err)
		}
		result[full] = fi.Sys().(*syscall.Stat_t).Ino
	}
	return result
}

// countCachedBlobs returns the number of LCC cache directories under cacheDir.
// The layout is cache/<algorithm>.<hex>.<uid>.<gid>.<seq>/; each direct child directory is one blob.
// ENOENT is treated as zero.
func countCachedBlobs(t *testing.T, cacheDir string) int {
	t.Helper()
	entries, err := os.ReadDir(cacheDir)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading cache dir: %v", err)
	}
	var count int
	for _, e := range entries {
		if e.IsDir() {
			count++
		}
	}
	return count
}
