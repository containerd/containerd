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
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/transfer"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDockerFetcher(hosts ...RegistryHost) dockerFetcher {
	if len(hosts) == 0 {
		hosts = []RegistryHost{
			{
				Host:         "localhost",
				Scheme:       "http",
				Path:         "/v2",
				Capabilities: HostCapabilityPull,
				Client:       http.DefaultClient,
			},
		}
	}
	return dockerFetcher{
		dockerBase: &dockerBase{
			hosts:      hosts,
			limiter:    nil,
			performances: transfer.ImageResolverPerformanceSettings{
				ConcurrentLayerFetchBuffer: 128 * 1024,
				MaxConcurrentDownloads:     3,
			},
		},
	}
}

func TestFetchCreatesActivityTracker(t *testing.T) {
	fetcher := newTestDockerFetcher()

	blobData := []byte("hello world")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
		w.Write(blobData)
	}))
	defer server.Close()

	fetcher.hosts[0].Host = mustParseHost(t, server.URL)
	fetcher.hosts[0].Scheme = "http"

	desc := ocispec.Descriptor{
		Digest:    digest.FromBytes(blobData),
		Size:      int64(len(blobData)),
		MediaType: "application/octet-stream",
	}

	ctx := context.Background()
	rc, err := fetcher.Fetch(ctx, desc)
	require.NoError(t, err)
	require.NotNil(t, rc)
	defer rc.Close()

	retrieved, ok := ActivityTrackerFromContext(ctx)
	assert.False(t, ok, "Original ctx should not be mutated by Fetch (Fetch returns new ctx internally)")
	assert.Nil(t, retrieved)

	buf, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, blobData, buf)
}

func TestFetchReusesExistingActivityTracker(t *testing.T) {
	existingTracker := NewActivityTrackerWithClock(&mockClock{now: time.Now().UnixNano()})
	ctx := WithActivityTracker(context.Background(), existingTracker)

	callCount := int32(0)
	blobData := []byte("tracked content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
		w.Write(blobData)
	}))
	defer server.Close()

	fetcher := newTestDockerFetcher()
	fetcher.hosts[0].Host = mustParseHost(t, server.URL)
	fetcher.hosts[0].Scheme = "http"

	desc := ocispec.Descriptor{
		Digest:    digest.FromBytes(blobData),
		Size:      int64(len(blobData)),
		MediaType: "application/octet-stream",
	}

	rc, err := fetcher.Fetch(ctx, desc)
	require.NoError(t, err)
	require.NotNil(t, rc)

	buf := make([]byte, len(blobData))
	_, readErr := rc.Read(buf)
	if readErr != nil && readErr != io.EOF {
		t.Fatalf("Read failed: %v", readErr)
	}
	rc.Close()

	assert.Equal(t, blobData, buf)
	assert.False(t, existingTracker.Stalled(5*time.Second), "Tracker should have been touched by the read")
}

func TestFetchByDigestCreatesActivityTracker(t *testing.T) {
	blobData := []byte("fetch by digest data")
	dgst := digest.FromBytes(blobData)

	headDone := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			atomic.AddInt32(&headDone, 1)
			w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
		w.Write(blobData)
	}))
	defer server.Close()

	fetcher := newTestDockerFetcher()
	fetcher.hosts[0].Host = mustParseHost(t, server.URL)
	fetcher.hosts[0].Scheme = "http"

	ctx := context.Background()
	rc, desc, err := fetcher.FetchByDigest(ctx, dgst)
	require.NoError(t, err)
	require.NotNil(t, rc)
	defer rc.Close()

	assert.Equal(t, dgst, desc.Digest)
	assert.Equal(t, int64(len(blobData)), desc.Size)

	buf, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, blobData, buf)
}

func TestFetchByDigestReusesExistingActivityTracker(t *testing.T) {
	existingTracker := NewActivityTrackerWithClock(&mockClock{now: time.Now().UnixNano()})
	ctx := WithActivityTracker(context.Background(), existingTracker)

	blobData := []byte("reused tracker data")
	dgst := digest.FromBytes(blobData)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
		w.Write(blobData)
	}))
	defer server.Close()

	fetcher := newTestDockerFetcher()
	fetcher.hosts[0].Host = mustParseHost(t, server.URL)
	fetcher.hosts[0].Scheme = "http"

	rc, desc, err := fetcher.FetchByDigest(ctx, dgst)
	require.NoError(t, err)
	require.NotNil(t, rc)

	buf := make([]byte, len(blobData))
	_, readErr := rc.Read(buf)
	if readErr != nil && readErr != io.EOF {
		t.Fatalf("Read failed: %v", readErr)
	}
	rc.Close()

	assert.Equal(t, dgst, desc.Digest)
	assert.Equal(t, blobData, buf)
	assert.False(t, existingTracker.Stalled(5*time.Second), "Existing tracker should be reused and touched")
}

func TestFetchActivityTrackerPassedToHTTPReadSeeker(t *testing.T) {
	tracker := &mockActivityTracker{}

	blobData := []byte("tracker passed to seeker")

	hrs, err := newHTTPReadSeeker(int64(len(blobData)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(blobData[offset:])), nil
	}, tracker)
	require.NoError(t, err)
	require.NotNil(t, hrs)
	defer hrs.Close()

	buf := make([]byte, len(blobData))
	n, err := hrs.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	assert.Equal(t, len(blobData), n)
	assert.Equal(t, blobData, buf)
	assert.Equal(t, int32(1), tracker.GetTouchCount(), "Touch() should be called once when Read returns data")
}

func TestFetchWithSlowServerCompletesWhenActivityExists(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	blobData := []byte(strings.Repeat("slow-server-data", 100))
	var chunksMu sync.Mutex
	var chunks [][]byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
		chunkSize := 64
		for i := 0; i < len(blobData); i += chunkSize {
			end := i + chunkSize
			if end > len(blobData) {
				end = len(blobData)
			}
			chunk := blobData[i:end]
			w.Write(chunk)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			chunksMu.Lock()
			chunks = append(chunks, chunk)
			chunksMu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer server.Close()

	tracker := NewActivityTrackerWithClock(&mockClock{now: time.Now().UnixNano()})
	ctx := WithActivityTracker(context.Background(), tracker)

	fetcher := newTestDockerFetcher()
	fetcher.hosts[0].Host = mustParseHost(t, server.URL)
	fetcher.hosts[0].Scheme = "http"

	dgst := digest.FromBytes(blobData)

	rc, desc, err := fetcher.FetchByDigest(ctx, dgst)
	require.NoError(t, err)
	require.NotNil(t, rc)

	buf, err := io.ReadAll(rc)
	require.NoError(t, err)
	rc.Close()

	assert.Equal(t, blobData, buf)
	assert.Equal(t, dgst, desc.Digest)
	assert.False(t, tracker.Stalled(5*time.Second), "Should not be stalled — data was flowing")
}

func TestFetchStalledConnectionTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	blobData := []byte("stalled-connection-data")

	stallTriggered := make(chan struct{})
	stallDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(blobData)))
		w.Write(blobData[:5])
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(stallTriggered)
		<-stallDone
	}))
	defer server.Close()

	clk := &mockClock{now: time.Now().UnixNano()}
	tracker := NewActivityTrackerWithClock(clk)
	tracker.Touch()

	ctx := WithActivityTracker(context.Background(), tracker)

	fetcher := newTestDockerFetcher()
	fetcher.hosts[0].Host = mustParseHost(t, server.URL)
	fetcher.hosts[0].Scheme = "http"

	dgst := digest.FromBytes(blobData)

	rc, _, err := fetcher.FetchByDigest(ctx, dgst)
	require.NoError(t, err)
	require.NotNil(t, rc)

	smallBuf := make([]byte, 10)
	n, _ := rc.Read(smallBuf)
	assert.Equal(t, 5, n, "Should read the initial data before stall")

	select {
	case <-stallTriggered:
	case <-time.After(5 * time.Second):
		t.Fatal("Server should have sent initial data")
	}

	clk.Advance(6 * time.Second)
	require.True(t, tracker.Stalled(5*time.Second), "Tracker should detect stall after advance")

	close(stallDone)
	rc.Close()
}
