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

package sys

import (
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/stretchr/testify/require"
)

// TestRequiresRoot registers the -test.root flag on Windows.
// The Linux test files in this package import testutil (which registers the
// flag), but they are not compiled on Windows. Without this, the Makefile's
// root-test target fails with "flag provided but not defined: -test.root".
func TestRequiresRoot(t *testing.T) {
	testutil.RequiresRoot(t)
}

type closeWriter interface {
	CloseWrite() error
}

func TestGetLocalListenerCloseWrite(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\containerd-test-%d`, rand.Int64())

	l, err := GetLocalListener(path, os.Getuid(), os.Getgid())
	require.NoError(t, err)
	defer l.Close()

	type result struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan result, 1)
	go func() {
		conn, err := l.Accept()
		accepted <- result{conn, err}
	}()

	client, err := winio.DialPipe(path, nil)
	require.NoError(t, err)
	defer client.Close()

	res := <-accepted
	require.NoError(t, res.err)
	serverConn := res.conn
	defer serverConn.Close()

	cw, ok := serverConn.(closeWriter)
	require.True(t, ok, "accepted connection %T does not implement CloseWrite", serverConn)

	require.NoError(t, cw.CloseWrite())

	// Client can still Write
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		client.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := client.Write(buf)
		errCh <- err
	}()
	// Server can still Read
	buf := make([]byte, 1)
	serverConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = serverConn.Read(buf)
	require.NoError(t, err)
	require.NoError(t, <-errCh)
}
