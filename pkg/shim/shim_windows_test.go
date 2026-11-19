//go:build windows

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

package shim

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/log"
)

const (
	// connectionWaitTime is the time to wait for pipe connections to be established
	connectionWaitTime = 50 * time.Millisecond
	// readTimeout is the timeout for reading from pipe connections
	readTimeout = time.Second
)

// testPipeCounter ensures unique pipe names across parallel tests
var testPipeCounter atomic.Uint64

// uniquePipePath generates a unique pipe path for testing
func uniquePipePath(prefix string) string {
	return fmt.Sprintf(`\\.\pipe\%s-%d-%d`, prefix, os.Getpid(), testPipeCounter.Add(1))
}

// createTestPipe creates a named pipe listener for testing. The caller is
// responsible for closing the returned listener.
func createTestPipe(t *testing.T, pipePath string) net.Listener {
	t.Helper()
	l, err := winio.ListenPipe(pipePath, nil)
	if err != nil {
		t.Fatalf("failed to create test pipe: %v", err)
	}
	return l
}

// connectToPipe dials the named pipe for testing. The caller is responsible
// for closing the returned connection.
func connectToPipe(t *testing.T, pipePath string) net.Conn {
	t.Helper()
	conn, err := winio.DialPipe(pipePath, nil)
	if err != nil {
		t.Fatalf("failed to connect to pipe: %v", err)
	}
	return conn
}

// readResult holds the result of an async read operation
type readResult struct {
	buf []byte
	err error
}

// asyncRead reads data from connection with timeout in a goroutine.
// Returns a channel that will receive the result when the read completes.
func asyncRead(conn net.Conn, expectedLen int) <-chan readResult {
	resultChan := make(chan readResult, 1)
	go func() {
		buf := make([]byte, expectedLen)
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		nRead, err := conn.Read(buf)
		resultChan <- readResult{buf: buf[:nRead], err: err}
	}()
	return resultChan
}

func TestSetupSignals(t *testing.T) {
	tests := []struct {
		name             string
		config           Config
		expectError      bool
		expectNilChan    bool
		expectedCapacity int
	}{
		{
			name:             "default config creates signal channel with capacity 32",
			config:           Config{},
			expectError:      false,
			expectNilChan:    false,
			expectedCapacity: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signals, err := setupSignals(tt.config)
			if (err != nil) != tt.expectError {
				t.Fatalf("setupSignals() error = %v, expectError %v", err, tt.expectError)
			}
			if (signals == nil) != tt.expectNilChan {
				t.Fatal("setupSignals returned unexpected nil channel state")
			}
			if signals != nil && cap(signals) != tt.expectedCapacity {
				t.Fatalf("expected signal channel capacity %d, got %d", tt.expectedCapacity, cap(signals))
			}
		})
	}
}

func TestServeListener(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		expectError bool
		shouldClose bool
	}{
		{
			name:        "empty path should fail",
			path:        "",
			expectError: true,
			shouldClose: false,
		},
		{
			name:        "non-pipe path should fail",
			path:        "/tmp/invalid/path",
			expectError: true,
			shouldClose: false,
		},
		{
			name:        "valid pipe path should succeed",
			path:        fmt.Sprintf(`\\.\pipe\containerd-shim-test-%d`, os.Getpid()),
			expectError: false,
			shouldClose: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := serveListener(tt.path, 0)
			if (err != nil) != tt.expectError {
				t.Fatalf("serveListener() error = %v, expectError %v", err, tt.expectError)
			}
			if tt.shouldClose && l != nil {
				defer l.Close()
			}
			if !tt.expectError && l == nil {
				t.Fatal("serveListener returned nil listener")
			}
		})
	}
}

func TestReconnectingLogWriterDropsLogsBeforeConnection(t *testing.T) {
	t.Parallel()
	pipePath := uniquePipePath("shim-test-log")
	l := createTestPipe(t, pipePath)

	rlw := &reconnectingLogWriter{l: l}
	go rlw.acceptConnections()
	defer rlw.Close()

	// Write before any client connects - should not block and return success
	testData := []byte("test log message before connection")
	n, err := rlw.Write(testData)
	if err != nil {
		t.Fatalf("Write should not return error before connection: %v", err)
	}
	if n != len(testData) {
		t.Fatalf("Write should return len(data) even when dropping: got %d, want %d", n, len(testData))
	}
}

func TestReconnectingLogWriterWritesAfterConnection(t *testing.T) {
	t.Parallel()
	pipePath := uniquePipePath("shim-test-log-write")
	l := createTestPipe(t, pipePath)

	rlw := &reconnectingLogWriter{l: l}
	go rlw.acceptConnections()
	defer rlw.Close()

	// Connect a client
	clientConn := connectToPipe(t, pipePath)
	defer clientConn.Close()

	// Give time for connection to be accepted
	time.Sleep(connectionWaitTime)

	// Write after client connects
	testData := []byte("test log message after connection")

	// Start reading from client side before writing to prevent blocking
	readChan := asyncRead(clientConn, len(testData))

	n, err := rlw.Write(testData)
	if err != nil {
		t.Fatalf("Write failed after connection: %v", err)
	}
	if n != len(testData) {
		t.Fatalf("Write returned wrong length: got %d, want %d", n, len(testData))
	}

	// Wait for read to complete and verify
	result := <-readChan
	if result.err != nil {
		t.Fatalf("client failed to read: %v", result.err)
	}
	if string(result.buf) != string(testData) {
		t.Fatalf("client read wrong data: got %q, want %q", string(result.buf), string(testData))
	}
}

func TestReconnectingLogWriterSupportsReconnection(t *testing.T) {
	t.Parallel()
	pipePath := uniquePipePath("shim-test-log-reconnect")
	l := createTestPipe(t, pipePath)

	rlw := &reconnectingLogWriter{l: l}
	go rlw.acceptConnections()
	defer rlw.Close()

	// First client connects
	client1 := connectToPipe(t, pipePath)

	// Give time for connection to be accepted
	time.Sleep(connectionWaitTime)

	// Write with first client
	testData1 := []byte("message to first client")

	// Start reading from first client before writing
	readChan1 := asyncRead(client1, len(testData1))

	_, err := rlw.Write(testData1)
	if err != nil {
		t.Fatalf("Write to first client failed: %v", err)
	}

	// Wait for read to complete and verify
	result1 := <-readChan1
	if result1.err != nil {
		t.Fatalf("first client failed to read: %v", result1.err)
	}
	if string(result1.buf) != string(testData1) {
		t.Fatalf("first client read wrong data: got %q, want %q", string(result1.buf), string(testData1))
	}

	// Second client connects (simulating containerd restart)
	client2 := connectToPipe(t, pipePath)
	defer client2.Close()

	// Give time for new connection to be accepted and old one closed
	time.Sleep(connectionWaitTime)

	// Close first client (it should already be closed by the writer)
	client1.Close()

	// Write with second client connected
	testData2 := []byte("message to second client")

	// Start reading from second client before writing
	readChan2 := asyncRead(client2, len(testData2))

	_, err = rlw.Write(testData2)
	if err != nil {
		t.Fatalf("Write to second client failed: %v", err)
	}

	// Wait for read to complete and verify
	result2 := <-readChan2
	if result2.err != nil {
		t.Fatalf("second client failed to read: %v", result2.err)
	}
	if string(result2.buf) != string(testData2) {
		t.Fatalf("second client read wrong data: got %q, want %q", string(result2.buf), string(testData2))
	}
}

func TestReconnectingLogWriterClose(t *testing.T) {
	t.Parallel()
	pipePath := uniquePipePath("shim-test-log-close")
	l := createTestPipe(t, pipePath)

	rlw := &reconnectingLogWriter{l: l}
	go rlw.acceptConnections()

	// Connect a client
	client := connectToPipe(t, pipePath)
	defer client.Close()

	// Give time for connection to be accepted
	time.Sleep(connectionWaitTime)

	// Close the writer
	err := rlw.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify listener is closed by trying to connect again
	_, err = winio.DialPipe(pipePath, nil)
	if err == nil {
		t.Fatal("should not be able to connect after Close")
	}
}

func TestReconnectingLogWriterConcurrentWrites(t *testing.T) {
	t.Parallel()
	pipePath := uniquePipePath("shim-test-log-concurrent")
	l := createTestPipe(t, pipePath)

	rlw := &reconnectingLogWriter{l: l}
	go rlw.acceptConnections()
	defer rlw.Close()

	// Connect a client
	client := connectToPipe(t, pipePath)
	defer client.Close()

	// Give time for connection to be accepted
	time.Sleep(connectionWaitTime)

	// Start reading in background
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, err := client.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Concurrent writes - collect errors instead of using t.Errorf in goroutine
	const numWriters = 10
	errChan := make(chan error, numWriters)
	var wg sync.WaitGroup
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := fmt.Sprintf("concurrent message %d\n", id)
			_, err := rlw.Write([]byte(msg))
			if err != nil {
				errChan <- fmt.Errorf("concurrent Write %d failed: %w", id, err)
			}
		}(i)
	}
	wg.Wait()
	close(errChan)

	// Report any errors from concurrent writes
	for err := range errChan {
		t.Error(err)
	}

	// Close and wait for reader to finish
	rlw.Close()
	<-readDone
}

func TestOpenLog(t *testing.T) {
	tests := []struct {
		name          string
		setupCtx      func() context.Context
		containerID   string
		expectError   bool
		shouldConnect bool
		pipePath      string
	}{
		{
			name: "creates named pipe and accepts connections",
			setupCtx: func() context.Context {
				return namespaces.WithNamespace(context.Background(), "test-ns")
			},
			containerID:   "test-container-id",
			expectError:   false,
			shouldConnect: true,
			pipePath:      `\\.\pipe\containerd-shim-test-ns-test-container-id-log`,
		},
		{
			name: "fails without namespace in context",
			setupCtx: func() context.Context {
				return context.Background()
			},
			containerID:   "test-container-id",
			expectError:   true,
			shouldConnect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupCtx()

			writer, err := openLog(ctx, tt.containerID)
			if (err != nil) != tt.expectError {
				t.Fatalf("openLog() error = %v, expectError %v", err, tt.expectError)
			}

			if tt.expectError {
				return
			}

			defer writer.(interface{ Close() error }).Close()

			if tt.shouldConnect {
				// Verify we can connect to the created pipe
				client, err := winio.DialPipe(tt.pipePath, nil)
				if err != nil {
					t.Fatalf("failed to connect to log pipe: %v", err)
				}
				defer client.Close()

				// Give time for connection to be accepted
				time.Sleep(connectionWaitTime)

				// Write should succeed
				testData := []byte("test log from openLog")

				// Read from client in goroutine to prevent blocking
				readDone := make(chan struct{})
				var readBuf []byte
				var readErr error
				go func() {
					defer close(readDone)
					buf := make([]byte, len(testData))
					_ = client.SetReadDeadline(time.Now().Add(readTimeout))
					nRead, err := client.Read(buf)
					readBuf = buf[:nRead]
					readErr = err
				}()

				n, err := writer.Write(testData)
				if err != nil {
					t.Fatalf("Write failed: %v", err)
				}
				if n != len(testData) {
					t.Fatalf("Write returned wrong length: got %d, want %d", n, len(testData))
				}

				// Wait for read to complete and verify client receives the data
				<-readDone
				if readErr != nil {
					t.Fatalf("client failed to read: %v", readErr)
				}
				if string(readBuf) != string(testData) {
					t.Fatalf("client read wrong data: got %q, want %q", string(readBuf), string(testData))
				}
			}
		})
	}
}

func TestSubreaper(t *testing.T) {
	// On Windows, subreaper is a no-op
	err := subreaper()
	if err != nil {
		t.Fatalf("subreaper should return nil on Windows: %v", err)
	}
}

// TestAwaitPipeReady_State1_RetriesOnErrTimeout proves that awaitPipeReady
// retries when winio.DialPipe returns winio.ErrTimeout (pipe in state 1:
// ListenPipe called, Accept not yet called).
//
// With the pre-fix code this test fails: awaitPipeReady returns winio.ErrTimeout
// immediately after the first dial attempt instead of retrying.
//
// After the fix (errors.Is(err, winio.ErrTimeout) treated as retryable) the
// test passes: the function retries past the timeout, Accept fires at 1.5 s,
// and the next dial attempt connects.
func TestAwaitPipeReady_State1_RetriesOnErrTimeout(t *testing.T) {
	pipePath := `\\.\pipe\` + strings.ReplaceAll(t.Name(), "/", "_")

	// Create the pipe listener (state 1): ListenPipe called, Accept not yet
	// called. winio.DialPipe against a state-1 pipe blocks for the per-attempt
	// timeout then returns winio.ErrTimeout — not context.DeadlineExceeded.
	listener, err := winio.ListenPipe(pipePath, nil)
	if err != nil {
		t.Fatalf("ListenPipe: %v", err)
	}
	defer listener.Close()

	// Transition to state 2 after 1.5 s — intentionally longer than the 1-second
	// per-attempt DialPipe timeout so the first attempt always returns ErrTimeout.
	// awaitPipeReady's 5-second overall timer still leaves enough room for a
	// successful retry once Accept fires.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	start := time.Now()
	if err := awaitPipeReady(pipePath); err != nil {
		t.Fatalf("awaitPipeReady(%q) = %v; want nil", pipePath, err)
	}
	// Assert at least one per-attempt timeout (1s) elapsed, proving the
	// retry path was exercised and the test is not a false positive from
	// Accept firing before the first dial attempt completed.
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("awaitPipeReady returned in %v; want ≥1s to confirm retry path was taken", elapsed)
	}
}

func TestAwaitPipeReadyEmptyAddress(t *testing.T) {
	// An empty address means "no pipe to wait for" and must return immediately.
	if err := awaitPipeReady(""); err != nil {
		t.Fatalf("awaitPipeReady(\"\") = %v; want nil", err)
	}
}

func TestAwaitPipeReadyConnectsWhenServing(t *testing.T) {
	t.Parallel()
	pipePath := uniquePipePath("shim-test-await-serving")
	l := createTestPipe(t, pipePath)
	defer l.Close()

	// A reader is actively accepting, so the dial should connect on the first try.
	go func() {
		conn, err := l.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	// Ensure Accept is pending before we dial.
	time.Sleep(connectionWaitTime)

	if err := awaitPipeReady(pipePath); err != nil {
		t.Fatalf("awaitPipeReady(%q) = %v; want nil", pipePath, err)
	}
}

func TestNewServer(t *testing.T) {
	srv, err := newServer()
	if err != nil {
		t.Fatalf("newServer() error = %v", err)
	}
	if srv == nil {
		t.Fatal("newServer() returned a nil server")
	}
}

func TestSetupDumpStacks(t *testing.T) {
	// setupDumpStacks is a no-op on Windows; it must return without panicking
	// or blocking on the supplied channel.
	setupDumpStacks(make(chan os.Signal, 1))
}

func TestReapReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- reap(ctx, log.L, make(chan os.Signal, 1))
	}()

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("reap() = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reap did not return after context cancellation")
	}
}

func TestReapContinuesAfterSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- reap(ctx, log.L, signals)
	}()

	// Delivering a signal must not stop the loop; on Windows reap just logs it.
	signals <- syscall.SIGTERM
	select {
	case err := <-errCh:
		t.Fatalf("reap returned unexpectedly after a signal: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Only context cancellation ends the loop.
	cancel()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("reap did not return after context cancellation")
	}
}

func TestHandleExitSignalsReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleExitSignals(ctx, log.L, func() {
			t.Error("shutdown callback should not run on context cancellation")
		})
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleExitSignals did not return after context cancellation")
	}
}

// shortWriteConn is a net.Conn whose Write reports fewer bytes than requested
// with a nil error, to exercise reconnectingLogWriter's short-write handling.
type shortWriteConn struct {
	net.Conn
	closed bool
}

func (c *shortWriteConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil // short write with nil error
}

func (c *shortWriteConn) Close() error {
	c.closed = true
	return nil
}

func TestReconnectingLogWriterDropsConnectionOnShortWrite(t *testing.T) {
	conn := &shortWriteConn{}
	rlw := &reconnectingLogWriter{conn: conn}

	data := []byte("hello world")
	n, err := rlw.Write(data)
	if err != nil {
		t.Fatalf("Write returned error on short write: %v", err)
	}
	if n != len(data) {
		t.Fatalf("Write returned n=%d; want %d (must report full success, never a short write)", n, len(data))
	}

	// A short write must drop the connection so the next write starts fresh.
	rlw.mu.Lock()
	dropped := rlw.conn == nil
	rlw.mu.Unlock()
	if !dropped {
		t.Fatal("short write should have dropped the connection")
	}
	if !conn.closed {
		t.Fatal("short write should have closed the dropped connection")
	}
}
