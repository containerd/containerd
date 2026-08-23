//go:build !windows

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

package io

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/containerd/v2/pkg/shim"
)

// startStreamServer serves a StreamManager over ttrpc and returns it with its address.
func startStreamServer(t *testing.T) (*shim.StreamManager, string) {
	t.Helper()

	// Unix socket paths are capped at ~100 bytes, so keep this one short.
	dir, err := os.MkdirTemp("", "pf")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")

	l, err := net.Listen("unix", socket)
	require.NoError(t, err)

	srv, err := ttrpc.NewServer()
	require.NoError(t, err)
	manager := shim.NewStreamManager()
	manager.RegisterTTRPC(srv)

	go srv.Serve(context.Background(), l)
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	return manager, fmt.Sprintf("ttrpc+unix://%s", socket)
}

// openPair opens both ends of a stream pair. waitCtx bounds the shim's wait for it,
// clientCtx the containerd side. Neither end is closed.
func openPair(t *testing.T, m *shim.StreamManager, address, id string, waitCtx, clientCtx context.Context) (client, shimSide io.ReadWriteCloser) {
	t.Helper()

	shimCh := make(chan io.ReadWriteCloser, 1)
	errCh := make(chan error, 1)
	go func() {
		s, err := m.OpenPortForward(waitCtx, id)
		if err != nil {
			errCh <- err
			return
		}
		shimCh <- s
	}()

	client, err := NewStreamPortForwardIO(clientCtx, address, id)
	require.NoError(t, err)

	select {
	case shimSide = <-shimCh:
	case err := <-errCh:
		t.Fatalf("OpenPortForward failed: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for the shim side of the stream pair")
	}
	return client, shimSide
}

// TestStreamPortForwardIO checks the stream pair carries data in both directions.
func TestStreamPortForwardIO(t *testing.T) {
	manager, address := startStreamServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clientSide, shimSide := openPair(t, manager, address, "portforward-test", ctx, ctx)
	defer clientSide.Close()
	defer shimSide.Close()

	// containerd -> shim
	_, err := clientSide.Write([]byte("request"))
	require.NoError(t, err)
	buf := make([]byte, 7)
	_, err = io.ReadFull(shimSide, buf)
	require.NoError(t, err)
	assert.Equal(t, "request", string(buf))

	// shim -> containerd
	_, err = shimSide.Write([]byte("respons"))
	require.NoError(t, err)
	_, err = io.ReadFull(clientSide, buf)
	require.NoError(t, err)
	assert.Equal(t, "respons", string(buf))
}

// TestStreamPortForwardIOOutlivesWaitDeadline checks the deadline a shim puts on the wait
// does not later tear down the forward it is carrying.
func TestStreamPortForwardIOOutlivesWaitDeadline(t *testing.T) {
	manager, address := startStreamServer(t)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer waitCancel()

	clientSide, shimSide := openPair(t, manager, address, "portforward-deadline", waitCtx, context.Background())
	defer clientSide.Close()
	defer shimSide.Close()

	<-waitCtx.Done()

	_, err := clientSide.Write([]byte("late"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(shimSide, buf)
	require.NoError(t, err)
	assert.Equal(t, "late", string(buf))
}

// readByteStreamGoroutines counts the goroutines ReadByteStream has running.
func readByteStreamGoroutines() int {
	buf := make([]byte, 1<<20)
	return strings.Count(string(buf[:runtime.Stack(buf, true)]), "streaming.ReadByteStream.func")
}

// TestStreamPortForwardIOTeardown checks closing a forward errors on neither end and
// reclaims the goroutines its byte streams started.
func TestStreamPortForwardIOTeardown(t *testing.T) {
	manager, address := startStreamServer(t)
	before := readByteStreamGoroutines()

	ctx, cancel := context.WithCancel(context.Background())
	clientSide, shimSide := openPair(t, manager, address, "portforward-teardown", context.Background(), ctx)

	require.NoError(t, shimSide.Close())
	require.NoError(t, clientSide.Close())
	// The containerd side ends with the caller's context, as sandboxPortForward does.
	cancel()

	assert.Eventually(t, func() bool {
		return readByteStreamGoroutines() <= before
	}, 10*time.Second, 20*time.Millisecond, "byte stream goroutines outlived the forward")
}

// TestStreamPortForwardIOBadAddress checks an unreachable endpoint surfaces an error
// rather than a half-open pipe.
func TestStreamPortForwardIOBadAddress(t *testing.T) {
	_, err := NewStreamPortForwardIO(context.Background(), "bogus://nowhere", "portforward-test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port forward input stream")
}
