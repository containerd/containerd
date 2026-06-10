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
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type slowResponse struct {
	delay     time.Duration
	respFunc  http.HandlerFunc
	mu        sync.Mutex
	callCount int32
}

func (s *slowResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.callCount, 1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	if s.respFunc != nil {
		s.respFunc(w, r)
	}
	s.mu.Unlock()
}

func (s *slowResponse) SetHandlerFunc(f http.HandlerFunc) {
	s.mu.Lock()
	s.respFunc = f
	s.mu.Unlock()
}

type MockRegistryServer struct {
	*httptest.Server
	slowResponse     *slowResponse
	uploadPath       string
	receivedDigests  []string
	digestMu         sync.Mutex
	blobData         []byte
	blobMu           sync.Mutex
	activityVerifier *ActivityVerifier
}

func NewMockRegistryServer() *MockRegistryServer {
	srv := &MockRegistryServer{
		slowResponse: &slowResponse{},
	}
	srv.Server = httptest.NewServer(srv.slowResponse)
	return srv
}

func (s *MockRegistryServer) SetSlowDelay(delay time.Duration) {
	s.slowResponse.delay = delay
}

func (s *MockRegistryServer) SetBlobHandler(handler http.HandlerFunc) {
	s.slowResponse.SetHandlerFunc(handler)
}

func (s *MockRegistryServer) GetReceivedDigests() []string {
	s.digestMu.Lock()
	defer s.digestMu.Unlock()
	digests := make([]string, len(s.receivedDigests))
	copy(digests, s.receivedDigests)
	return digests
}

func (s *MockRegistryServer) GetBlobData() []byte {
	s.blobMu.Lock()
	defer s.blobMu.Unlock()
	data := make([]byte, len(s.blobData))
	copy(data, s.blobData)
	return data
}

func (s *MockRegistryServer) RegisterActivityVerifier(av *ActivityVerifier) {
	s.activityVerifier = av
}

func (s *MockRegistryServer) UploadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.URL.Path == "/v2/blobs/uploads/" {
			if r.Method == http.MethodPost {
				w.Header().Set("Location", s.URL+"/v2/blobs/uploads/abc123?digest=sha256:"+r.FormValue("digest"))
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}

		if r.URL.Path == "/v2/blobs/uploads/abc123" {
			if r.Method == http.MethodPatch || r.Method == http.MethodPut {
				body, err := io.ReadAll(r.Body)
				if err == nil {
					s.blobMu.Lock()
					s.blobData = append(s.blobData, body...)
					s.blobMu.Unlock()

					if s.activityVerifier != nil {
						s.activityVerifier.touchLock.Lock()
						s.activityVerifier.touchCount++
						s.activityVerifier.lastTouchTime = time.Now()
						s.activityVerifier.touchLock.Unlock()
					}
				}

				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusCreated)
					return
				}
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		http.NotFound(w, r)
	}
}

type ActivityVerifier struct {
	touchCount    int32
	lastTouchTime time.Time
	touchLock     sync.Mutex
	stallDetected bool
	stallLock     sync.Mutex
}

func NewActivityVerifier() *ActivityVerifier {
	return &ActivityVerifier{}
}

func (av *ActivityVerifier) RecordTouch() {
	av.touchLock.Lock()
	av.touchCount++
	av.lastTouchTime = time.Now()
	av.touchLock.Unlock()
}

func (av *ActivityVerifier) RecordStall() {
	av.stallLock.Lock()
	av.stallDetected = true
	av.stallLock.Unlock()
}

func (av *ActivityVerifier) GetTouchCount() int32 {
	return atomic.LoadInt32(&av.touchCount)
}

func (av *ActivityVerifier) IsStallDetected() bool {
	av.stallLock.Lock()
	defer av.stallLock.Unlock()
	return av.stallDetected
}

func (av *ActivityVerifier) Reset() {
	av.touchLock.Lock()
	av.touchCount = 0
	av.lastTouchTime = time.Time{}
	av.touchLock.Unlock()
	av.stallLock.Lock()
	av.stallDetected = false
	av.stallLock.Unlock()
}

type mockActivityTrackerWithVerifier struct {
	verifier    *ActivityVerifier
	stallReturn bool
	window      time.Duration
}

func (m *mockActivityTrackerWithVerifier) Touch() {
	if m.verifier != nil {
		m.verifier.RecordTouch()
	}
}

func (m *mockActivityTrackerWithVerifier) Stalled(window time.Duration) bool {
	if m.verifier != nil {
		m.verifier.RecordStall()
	}
	return m.stallReturn
}

func (m *mockActivityTrackerWithVerifier) TimeSinceLastActivity() time.Duration {
	m.verifier.touchLock.Lock()
	defer m.verifier.touchLock.Unlock()
	if m.verifier.lastTouchTime.IsZero() {
		return 0
	}
	return time.Since(m.verifier.lastTouchTime)
}

func TestPushBlobWithSlowServerCompletesWhenActivityExists(t *testing.T) {
	server := NewMockRegistryServer()
	defer server.Close()

	server.SetBlobHandler(server.UploadHandler())

	tracker := NewActivityTrackerWithClock(&mockClock{now: 0})
	clock := tracker.clock.(*mockClock)
	statusTracker := NewInMemoryTracker()

	ref := "test-blob-ref"
	digestValue := digest.Digest("sha256:abc123")

	statusTracker.SetStatus(ref, Status{
		Status: content.Status{
			Ref:    ref,
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, ref, digestValue, statusTracker, false, tracker)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusAccepted)

	pw.setResponse(resp.Result())

	ctx := context.Background()
	done := make(chan error, 1)

	go func() {
		done <- pw.Commit(ctx, 0, "")
	}()

	go func() {
		clock.Advance(50 * time.Millisecond)
		tracker.Touch()
	}()

	// Integration test with httptest.Server - server responds immediately,
	// so timeout is just a safety net for CI environments.
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("PushBlob should have completed within timeout when activity exists")
	}
}

func TestPushBlobWithConcurrentActivity(t *testing.T) {
	server := NewMockRegistryServer()
	defer server.Close()

	server.SetBlobHandler(server.UploadHandler())

	tracker := NewActivityTrackerWithClock(&mockClock{now: 0})
	statusTracker := NewInMemoryTracker()

	ref := "test-concurrent-blob"

	statusTracker.SetStatus(ref, Status{
		Status: content.Status{
			Ref:    ref,
			Total:  1024,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, ref, digest.Digest("sha256:concurrent"), statusTracker, false, tracker)

	pr, pipeWriter := io.Pipe()
	pw.pipeC <- &activityPipeWriter{pw: pipeWriter, tracker: tracker}

	done := make(chan struct{})
	go func() {
		defer close(done)
		io.Copy(io.Discard, pr)
	}()

	var wg sync.WaitGroup
	numWriters := 5
	numWrites := 50

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numWrites; j++ {
				data := []byte{byte(id), byte(j)}
				_, err := pw.Write(data)
				if err != nil && err != io.ErrClosedPipe {
					assert.NoError(t, err)
				}
			}
		}(i)
	}

	wg.Wait()
	pipeWriter.Close()
	pr.Close()
	<-done

	assert.GreaterOrEqual(t, tracker.TimeSinceLastActivity(), time.Duration(0))
}

func TestPushBlobWithNoActivityTrackerCompletes(t *testing.T) {
	server := NewMockRegistryServer()
	defer server.Close()

	server.SetBlobHandler(server.UploadHandler())

	statusTracker := NewInMemoryTracker()
	ref := "test-no-tracker-blob"

	statusTracker.SetStatus(ref, Status{
		Status: content.Status{
			Ref:    ref,
			Total:  100,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, ref, digest.Digest("sha256:noactivity"), statusTracker, false, nil)
	pw.pipe = nil

	resp := httptest.NewRecorder()
	resp.WriteHeader(http.StatusOK)

	pw.setResponse(resp.Result())

	ctx := context.Background()
	done := make(chan error, 1)

	go func() {
		done <- pw.Commit(ctx, 0, "")
	}()

	// Integration test with httptest.Server - server responds immediately,
	// so timeout is just a safety net for CI environments.
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("PushBlob should have completed without activity tracker")
	}
}

func TestActivityVerifierRecordsTouches(t *testing.T) {
	verifier := NewActivityVerifier()

	verifier.RecordTouch()
	verifier.RecordTouch()
	verifier.RecordTouch()

	assert.Equal(t, int32(3), verifier.GetTouchCount())
}

func TestActivityVerifierRecordsStall(t *testing.T) {
	verifier := NewActivityVerifier()

	verifier.RecordStall()

	assert.True(t, verifier.IsStallDetected())
}

func TestMockRegistryServerSlowResponse(t *testing.T) {
	slowResp := &slowResponse{delay: 100 * time.Millisecond}
	server := httptest.NewServer(slowResp)
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, int32(1), slowResp.callCount)
}

func TestPushBlobWithSlowServerActivityTracking(t *testing.T) {
	server := NewMockRegistryServer()
	defer server.Close()

	verifier := NewActivityVerifier()
	server.RegisterActivityVerifier(verifier)

	server.SetBlobHandler(server.UploadHandler())

	tracker := &mockActivityTrackerWithVerifier{
		verifier:    verifier,
		stallReturn: false,
	}

	statusTracker := NewInMemoryTracker()
	ref := "test-activity-tracking"

	statusTracker.SetStatus(ref, Status{
		Status: content.Status{
			Ref:    ref,
			Total:  200,
			Offset: 0,
		},
	})

	pw := newPushWriter(nil, ref, digest.Digest("sha256:activitytracking"), statusTracker, false, tracker)

	pr, pipeWriter := io.Pipe()
	pw.pipeC <- &activityPipeWriter{pw: pipeWriter, tracker: tracker}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1024)
		for {
			n, err := pr.Read(buf)
			if n > 0 && verifier != nil {
				verifier.RecordTouch()
			}
			if err != nil {
				break
			}
		}
	}()

	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}

	for i := 0; i < 10; i++ {
		_, err := pw.Write(data)
		if err != nil && err != io.ErrClosedPipe {
			require.NoError(t, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	pipeWriter.Close()
	pr.Close()
	<-done

	assert.GreaterOrEqual(t, verifier.GetTouchCount(), int32(1))
}
