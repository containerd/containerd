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

package server

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialPodIPs(t *testing.T) {
	// Start a local TCP listener to serve as a mock target.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	_, portStr, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	portInt, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	port := int32(portInt)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	t.Run("Success", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, err := dialPodIPs(ctx, []string{"127.0.0.1"}, port)
		require.NoError(t, err)
		assert.NotNil(t, conn)
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("Fallback", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// 127.0.0.254 on a non-listening port will fail, falling back to 127.0.0.1
		// Use a port where nothing is listening for 127.0.0.254, or an IP that fails quickly
		conn, err := dialPodIPs(ctx, []string{"127.0.0.254", "127.0.0.1"}, port)
		require.NoError(t, err)
		assert.NotNil(t, conn)
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("NoPodIPsProvided", func(t *testing.T) {
		ctx := context.Background()
		_, err := dialPodIPs(ctx, nil, port)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pod IPs provided")

		_, err = dialPodIPs(ctx, []string{""}, port)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pod IPs provided")
	})

	t.Run("AllFailed", func(t *testing.T) {
		// Pick an unlistened port
		unlistenedListener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		_, unlistenedPortStr, err := net.SplitHostPort(unlistenedListener.Addr().String())
		require.NoError(t, err)
		unlistenedPortInt, err := strconv.Atoi(unlistenedPortStr)
		require.NoError(t, err)
		unlistenedPort := int32(unlistenedPortInt)
		// Close listener so port is free but not accepting connections
		unlistenedListener.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		_, err = dialPodIPs(ctx, []string{"127.0.0.1"}, unlistenedPort)
		require.Error(t, err)
	})

	t.Run("CanceledContext", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := dialPodIPs(ctx, []string{"127.0.0.1"}, port)
		require.Error(t, err)
	})
}

func TestDialLocalhost(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()

	_, portStr, err := net.SplitHostPort(l.Addr().String())
	require.NoError(t, err)
	portInt, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	port := int32(portInt)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	t.Run("Success", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, err := dialLocalhost(ctx, port)
		require.NoError(t, err)
		assert.NotNil(t, conn)
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("Failure", func(t *testing.T) {
		unlistedL, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		_, unlistenedPortStr, err := net.SplitHostPort(unlistedL.Addr().String())
		require.NoError(t, err)
		unlistenedPortInt, err := strconv.Atoi(unlistenedPortStr)
		require.NoError(t, err)
		unlistedL.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		_, err = dialLocalhost(ctx, int32(unlistenedPortInt))
		require.Error(t, err)
	})
}

func TestIsVMBasedRuntime(t *testing.T) {
	testCases := []struct {
		runtimeType string
		expected    bool
	}{
		{runtimeType: "io.containerd.kata.v2", expected: true},
		{runtimeType: "io.containerd.kata-qemu.v2", expected: true},
		{runtimeType: "io.containerd.kata-fc.v2", expected: true},
		{runtimeType: "io.containerd.runc.v2", expected: false},
		{runtimeType: "io.containerd.runc.v1", expected: false},
		{runtimeType: "", expected: false},
	}

	for _, tc := range testCases {
		t.Run(tc.runtimeType, func(t *testing.T) {
			assert.Equal(t, tc.expected, isVMBasedRuntime(tc.runtimeType))
		})
	}
}

func TestSkipLocalhostForVMBasedRuntime(t *testing.T) {
	testCases := []struct {
		name         string
		runtimeType  string
		podIPs       []string
		expectedSkip bool
	}{
		{
			name:         "VM runtime with pod IPs skips localhost",
			runtimeType:  "io.containerd.kata.v2",
			podIPs:       []string{"10.244.0.5"},
			expectedSkip: true,
		},
		{
			name:         "VM runtime without pod IPs does not skip localhost",
			runtimeType:  "io.containerd.kata.v2",
			podIPs:       nil,
			expectedSkip: false,
		},
		{
			name:         "Process runtime with pod IPs does not skip localhost",
			runtimeType:  "io.containerd.runc.v2",
			podIPs:       []string{"10.244.0.5"},
			expectedSkip: false,
		},
		{
			name:         "Process runtime without pod IPs does not skip localhost",
			runtimeType:  "io.containerd.runc.v2",
			podIPs:       nil,
			expectedSkip: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			skipLocalhost := len(tc.podIPs) > 0 && isVMBasedRuntime(tc.runtimeType)
			assert.Equal(t, tc.expectedSkip, skipLocalhost)
		})
	}
}

func TestLocalhostFailureFallbackToPodIP(t *testing.T) {
	// Start a listener on 127.0.0.1 to simulate the pod IP endpoint succeeding
	podListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer podListener.Close()

	_, podPortStr, err := net.SplitHostPort(podListener.Addr().String())
	require.NoError(t, err)
	podPortInt, err := strconv.Atoi(podPortStr)
	require.NoError(t, err)
	port := int32(podPortInt)

	go func() {
		for {
			conn, err := podListener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Reserved unlistened port for localhost failure simulation
	unlistedL, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, unlistedPortStr, err := net.SplitHostPort(unlistedL.Addr().String())
	require.NoError(t, err)
	unlistedPortInt, err := strconv.Atoi(unlistedPortStr)
	require.NoError(t, err)
	unlistedL.Close()

	unlistedPort := int32(unlistedPortInt)

	t.Run("Localhost Fails and Fallback to Pod IP Succeeds", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// 1. Attempt dialLocalhost on unlistened port -> should fail
		_, err := dialLocalhost(ctx, unlistedPort)
		require.Error(t, err)

		// 2. Fall back to dialPodIPs on listening port -> should succeed
		conn, err := dialPodIPs(ctx, []string{"127.0.0.1"}, port)
		require.NoError(t, err)
		assert.NotNil(t, conn)
		if conn != nil {
			conn.Close()
		}
	})
}
