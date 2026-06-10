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
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockActivityTracker struct {
	touchCount    int32
	touchTimes    []time.Time
	mu            sync.Mutex
	stalledReturn bool
}

func (m *mockActivityTracker) Touch() {
	atomic.AddInt32(&m.touchCount, 1)
	m.mu.Lock()
	m.touchTimes = append(m.touchTimes, time.Now())
	m.mu.Unlock()
}

func (m *mockActivityTracker) Stalled(window time.Duration) bool {
	return m.stalledReturn
}

func (m *mockActivityTracker) TimeSinceLastActivity() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.touchTimes) == 0 {
		return 0
	}
	return time.Since(m.touchTimes[len(m.touchTimes)-1])
}

func (m *mockActivityTracker) GetTouchCount() int32 {
	return atomic.LoadInt32(&m.touchCount)
}

func TestHTTPReadSeekerReadCallsTouchWhenActivityTrackerPresent(t *testing.T) {
	tracker := &mockActivityTracker{}

	data := []byte("test data content")

	hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}, tracker)
	if err != nil {
		t.Fatalf("newHTTPReadSeeker failed: %v", err)
	}
	defer hrs.Close()

	buf := make([]byte, 1024)
	n, err := hrs.Read(buf)

	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected to read %d bytes, got %d", len(data), n)
	}

	if tracker.GetTouchCount() == 0 {
		t.Error("Touch() should have been called when activity tracker is present and Read returns data")
	}
}

func TestHTTPReadSeekerReadDoesNotCallTouchWhenActivityTrackerNil(t *testing.T) {
	data := []byte("test data content")

	hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	if err != nil {
		t.Fatalf("newHTTPReadSeeker failed: %v", err)
	}
	defer hrs.Close()

	buf := make([]byte, 1024)
	n, err := hrs.Read(buf)

	if err != nil && err != io.EOF {
		t.Fatalf("Read failed: %v", err)
	}

	if n != len(data) {
		t.Fatalf("Expected to read %d bytes, got %d", len(data), n)
	}
}

func TestHTTPReadSeekerReadDoesNotCallTouchOnZeroRead(t *testing.T) {
	tracker := &mockActivityTracker{}

	data := []byte("test")

	hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}, tracker)
	if err != nil {
		t.Fatalf("newHTTPReadSeeker failed: %v", err)
	}
	defer hrs.Close()

	buf := make([]byte, 0)
	_, _ = hrs.Read(buf)

	if tracker.GetTouchCount() > 0 {
		t.Error("Touch() should not have been called when Read returns 0 bytes")
	}
}

func TestHTTPReadSeekerMultipleReadsWithActivityTracking(t *testing.T) {
	tracker := &mockActivityTracker{}

	data := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")

	hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}, tracker)
	if err != nil {
		t.Fatalf("newHTTPReadSeeker failed: %v", err)
	}
	defer hrs.Close()

	buf := make([]byte, 5)
	_, _ = hrs.Read(buf)

	_, _ = hrs.Read(buf)
	_, _ = hrs.Read(buf)

	expectedTouches := int32(3)
	if tracker.GetTouchCount() < expectedTouches {
		t.Errorf("Expected at least %d Touch() calls, got %d", expectedTouches, tracker.GetTouchCount())
	}
}

func TestHTTPReadSeekerReadAfterClose(t *testing.T) {
	tracker := &mockActivityTracker{}

	data := []byte("test data")

	hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}, tracker)
	if err != nil {
		t.Fatalf("newHTTPReadSeeker failed: %v", err)
	}

	hrs.Close()

	buf := make([]byte, 1024)
	n, err := hrs.Read(buf)

	if err != io.EOF {
		t.Errorf("Expected io.EOF after Close, got err=%v", err)
	}

	if n != 0 {
		t.Errorf("Expected 0 bytes after Close, got %d", n)
	}
}

func TestHTTPReadSeekerSeekAndReadWithActivityTracker(t *testing.T) {
	tracker := &mockActivityTracker{}

	data := []byte("0123456789ABCDEFGHIJ")

	hrs := &httpReadSeeker{
		size:   int64(len(data)),
		open: func(offset int64) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data[offset:])), nil
		},
		activity: tracker,
	}
	defer hrs.Close()

	buf := make([]byte, 5)
	hrs.Read(buf)

	hrs.Seek(5, 0)

	hrs.Read(buf)

	if tracker.GetTouchCount() < 2 {
		t.Errorf("Expected at least 2 Touch() calls after Seek and Read, got %d", tracker.GetTouchCount())
	}
}

func TestHTTPReadSeekerActivityTrackerWithRealActivityTracker(t *testing.T) {
	activity := NewActivityTracker(5 * time.Second)

	data := []byte("test data content")

	hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}, activity)
	if err != nil {
		t.Fatalf("newHTTPReadSeeker failed: %v", err)
	}
	defer hrs.Close()

	buf := make([]byte, 1024)
	_, _ = hrs.Read(buf)

	if activity.TimeSinceLastActivity() == 0 {
		t.Error("TimeSinceLastActivity() should not be 0 after Read with activity tracker")
	}
}

func TestHTTPReadSeekerConcurrentReadsDifferentSeekerInstances(t *testing.T) {
	tracker := &mockActivityTracker{}

	var wg sync.WaitGroup
	numGoroutines := 10
	numReads := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			data := make([]byte, 1024)
			for j := range data {
				data[j] = byte((id + j) % 256)
			}

			hrs, err := newHTTPReadSeeker(int64(len(data)), func(offset int64) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(data)), nil
			}, tracker)
			if err != nil {
				t.Errorf("newHTTPReadSeeker failed: %v", err)
				return
			}

			buf := make([]byte, 64)
			for j := 0; j < numReads; j++ {
				_, err := hrs.Read(buf)
				if err != nil && err != io.EOF {
					t.Errorf("Read failed: %v", err)
					break
				}
			}
			hrs.Close()
		}(i)
	}

	wg.Wait()

	if tracker.GetTouchCount() == 0 {
		t.Error("Touch() should have been called during concurrent reads")
	}
}
