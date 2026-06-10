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

package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithActivityTrackerAndFromContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	tracker := NewActivityTracker(time.Second)

	ctx = WithActivityTracker(ctx, tracker)
	retrieved, ok := ActivityTrackerFromContext(ctx)

	require.True(t, ok, "ActivityTrackerFromContext should return true when tracker is set")
	assert.Same(t, tracker, retrieved, "Retrieved tracker should be the same as the one set")
}

func TestActivityTrackerFromContextReturnsFalseWhenNotSet(t *testing.T) {
	ctx := context.Background()
	retrieved, ok := ActivityTrackerFromContext(ctx)

	assert.False(t, ok, "ActivityTrackerFromContext should return false when no tracker is set")
	assert.Nil(t, retrieved, "Retrieved tracker should be nil when not set")
}

func TestRequestDoUsesActivityTrackerWhenPresent(t *testing.T) {
	tracker := NewActivityTracker(time.Second)
	ctx := WithActivityTracker(context.Background(), tracker)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := &request{
		method: http.MethodGet,
		path:   "/test",
		header: http.Header{},
		host: RegistryHost{
			Host:   server.URL[7:], // strip "http://"
			Scheme: "http",
			Path:   "/v2",
		},
		size: 100, // size > 0 to trigger activity tracker logic
	}

	// Create a custom client with a transport that has ResponseHeaderTimeout set
	originalTransport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	req.host.Client = &http.Client{
		Transport: originalTransport,
	}

	resp, err := req.do(ctx)
	require.NoError(t, err)
	resp.Body.Close()

	// When activity tracker is in context and size > 0, the cloned transport used by the
	// request should have ResponseHeaderTimeout disabled (0), but the original transport
	// should remain unchanged to avoid race conditions with concurrent requests.
	// Before the fix, originalTransport would have been modified directly (race condition).
	// After the fix, originalTransport remains unchanged.
	assert.Equal(t, 30*time.Second, originalTransport.ResponseHeaderTimeout,
		"Original transport's ResponseHeaderTimeout should remain unchanged (30s) after fix")
}

func TestRequestDoUsesDefaultTimeoutWhenNoActivityTracker(t *testing.T) {
	ctx := context.Background() // no activity tracker

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := &request{
		method: http.MethodGet,
		path:   "/test",
		header: http.Header{},
		host: RegistryHost{
			Host:   server.URL[7:], // strip "http://"
			Scheme: "http",
			Path:   "/v2",
		},
		size: 100, // size > 0
	}

	// Create a custom client with a transport that has a ResponseHeaderTimeout
	req.host.Client = &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}

	resp, err := req.do(ctx)
	require.NoError(t, err)
	resp.Body.Close()

	// When no activity tracker, ResponseHeaderTimeout should remain unchanged
	transport, ok := req.host.Client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, 30*time.Second, transport.ResponseHeaderTimeout,
		"ResponseHeaderTimeout should remain unchanged when no activity tracker is present")
}

func TestActivityTrackerContextKeyDoesNotCollide(t *testing.T) {
	// Set a tracker
	tracker1 := NewActivityTracker(time.Second)
	ctx1 := WithActivityTracker(context.Background(), tracker1)

	// Set a different value with the same context key type
	type mockValue struct {
		value string
	}
	ctx2 := context.WithValue(ctx1, activityTrackerContextKey{}, &mockValue{value: "test"})

	// ctx1 should still have the tracker
	retrieved1, ok1 := ActivityTrackerFromContext(ctx1)
	assert.True(t, ok1)
	assert.Same(t, tracker1, retrieved1)

	// ctx2 should have the mockValue, but ActivityTrackerFromContext should return ok=false
	// because mockValue doesn't implement ActivityTrackerInterface
	retrieved2, ok2 := ActivityTrackerFromContext(ctx2)
	assert.False(t, ok2, "Should return false when value is not ActivityTrackerInterface")
	assert.Nil(t, retrieved2)

	// But we can still retrieve the mockValue directly using the context key
	mock, ok := ctx2.Value(activityTrackerContextKey{}).(*mockValue)
	assert.True(t, ok)
	assert.Equal(t, "test", mock.value)
}

func TestRequestDoWithNilTransport(t *testing.T) {
	tracker := NewActivityTracker(time.Second)
	ctx := WithActivityTracker(context.Background(), tracker)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := &request{
		method: http.MethodGet,
		path:   "/test",
		header: http.Header{},
		host: RegistryHost{
			Host:   server.URL[7:],
			Scheme: "http",
			Path:   "/v2",
		},
		size: 100,
	}

	// Create a client with nil transport (uses default)
	req.host.Client = &http.Client{}

	resp, err := req.do(ctx)
	require.NoError(t, err)
	resp.Body.Close()

	// Should not panic and should work fine
}

func TestRequestDoWithActivityTrackerAndNoHostClient(t *testing.T) {
	tracker := NewActivityTracker(time.Second)
	ctx := WithActivityTracker(context.Background(), tracker)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := &request{
		method: http.MethodGet,
		path:   "/test",
		header: http.Header{},
		host: RegistryHost{
			Host:   server.URL[7:],
			Scheme: "http",
			Path:   "/v2",
		},
		size: 100,
	}

	// No host.Client set - should use default http.Client
	resp, err := req.do(ctx)
	require.NoError(t, err)
	resp.Body.Close()
}