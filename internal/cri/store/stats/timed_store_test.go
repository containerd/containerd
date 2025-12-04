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

package stats

import (
	"testing"
	"time"
)

func TestTimedStoreBasic(t *testing.T) {
	store := NewTimedStore(time.Minute, 10)

	// Initially empty
	if store.Size() != 0 {
		t.Errorf("expected size 0, got %d", store.Size())
	}

	// GetLatest should return nil when empty
	if sample := store.GetLatest(); sample != nil {
		t.Errorf("expected nil sample, got %v", sample)
	}

	// GetLatestUsageNanoCores should return false when empty
	if _, ok := store.GetLatestUsageNanoCores(); ok {
		t.Error("expected false for empty store")
	}
}

func TestTimedStoreAddAndGet(t *testing.T) {
	store := NewTimedStore(time.Minute, 10)

	now := time.Now()

	// Add first sample
	store.Add(now, 1000000000) // 1 second of CPU time in nanoseconds

	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}

	sample := store.GetLatest()
	if sample == nil {
		t.Fatal("expected non-nil sample")
	}
	if sample.UsageCoreNanoSeconds != 1000000000 {
		t.Errorf("expected UsageCoreNanoSeconds 1000000000, got %d", sample.UsageCoreNanoSeconds)
	}
	// First sample should have 0 UsageNanoCores (no previous sample to calculate from)
	if sample.UsageNanoCores != 0 {
		t.Errorf("expected UsageNanoCores 0 for first sample, got %d", sample.UsageNanoCores)
	}

	// GetLatestUsageNanoCores should still return false (need at least 2 samples)
	if _, ok := store.GetLatestUsageNanoCores(); ok {
		t.Error("expected false with only 1 sample")
	}
}

func TestTimedStoreUsageNanoCoresCalculation(t *testing.T) {
	store := NewTimedStore(time.Minute, 10)

	now := time.Now()

	// Add first sample: 1 second of CPU time
	store.Add(now, 1000000000)

	// Add second sample after 1 second: 2 seconds of CPU time
	// This means the CPU was running at 100% (1 core) during that second
	store.Add(now.Add(time.Second), 2000000000)

	if store.Size() != 2 {
		t.Errorf("expected size 2, got %d", store.Size())
	}

	// Now we should be able to get UsageNanoCores
	usageNanoCores, ok := store.GetLatestUsageNanoCores()
	if !ok {
		t.Fatal("expected true with 2 samples")
	}

	// Expected: (2000000000 - 1000000000) / 1000000000 * 1000000000 = 1000000000 nanocores (1 core)
	expected := uint64(1000000000)
	if usageNanoCores != expected {
		t.Errorf("expected UsageNanoCores %d, got %d", expected, usageNanoCores)
	}
}

func TestTimedStoreHalfCoreUsage(t *testing.T) {
	store := NewTimedStore(time.Minute, 10)

	now := time.Now()

	// Add first sample
	store.Add(now, 0)

	// Add second sample after 2 seconds: 1 second of CPU time
	// This means the CPU was running at 50% (0.5 cores) during those 2 seconds
	store.Add(now.Add(2*time.Second), 1000000000)

	usageNanoCores, ok := store.GetLatestUsageNanoCores()
	if !ok {
		t.Fatal("expected true with 2 samples")
	}

	// Expected: 1000000000 / 2000000000 * 1000000000 = 500000000 nanocores (0.5 cores)
	expected := uint64(500000000)
	if usageNanoCores != expected {
		t.Errorf("expected UsageNanoCores %d, got %d", expected, usageNanoCores)
	}
}

func TestTimedStoreMaxItems(t *testing.T) {
	store := NewTimedStore(time.Hour, 3) // Keep at most 3 samples

	now := time.Now()

	// Add 5 samples
	for i := 0; i < 5; i++ {
		store.Add(now.Add(time.Duration(i)*time.Second), uint64(i)*1000000000)
	}

	// Should only have 3 samples
	if store.Size() != 3 {
		t.Errorf("expected size 3, got %d", store.Size())
	}

	// Latest sample should be the 5th one (index 4)
	sample := store.GetLatest()
	if sample == nil {
		t.Fatal("expected non-nil sample")
	}
	if sample.UsageCoreNanoSeconds != 4000000000 {
		t.Errorf("expected UsageCoreNanoSeconds 4000000000, got %d", sample.UsageCoreNanoSeconds)
	}
}

func TestTimedStoreAgeEviction(t *testing.T) {
	store := NewTimedStore(3*time.Second, -1) // Keep samples for 3 seconds, no item limit

	now := time.Now()

	// Add sample at t=0
	store.Add(now, 0)

	// Add sample at t=1s
	store.Add(now.Add(1*time.Second), 1000000000)

	// Add sample at t=2s
	store.Add(now.Add(2*time.Second), 2000000000)

	if store.Size() != 3 {
		t.Errorf("expected size 3, got %d", store.Size())
	}

	// Add sample at t=4s - this should evict t=0 sample only
	// evictTime = t=4s - 3s = t=1s, so samples strictly after t=1s are kept (t=2s, t=4s)
	store.Add(now.Add(4*time.Second), 4000000000)

	// Should have t=2s and t=4s samples (samples strictly after evictTime t=1s)
	if store.Size() != 2 {
		t.Errorf("expected size 2 after eviction, got %d", store.Size())
	}

	// Verify latest sample is the one we just added
	sample := store.GetLatest()
	if sample == nil {
		t.Fatal("expected non-nil sample")
	}
	if sample.UsageCoreNanoSeconds != 4000000000 {
		t.Errorf("expected UsageCoreNanoSeconds 4000000000, got %d", sample.UsageCoreNanoSeconds)
	}
}

func TestTimedStoreConcurrentAccess(t *testing.T) {
	store := NewTimedStore(time.Minute, 100)

	now := time.Now()
	done := make(chan bool)

	// Start multiple goroutines adding samples
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				store.Add(now.Add(time.Duration(id*100+j)*time.Millisecond), uint64(id*100+j)*1000000)
			}
			done <- true
		}(i)
	}

	// Start multiple goroutines reading
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				store.GetLatest()
				store.GetLatestUsageNanoCores()
				store.Size()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Just verify no panic occurred and size is reasonable
	if store.Size() > 100 {
		t.Errorf("expected size <= 100, got %d", store.Size())
	}
}

func TestCalculateUsageNanoCores(t *testing.T) {
	// Use a fixed base time to ensure consistent time deltas in tests
	now := time.Now()

	tests := []struct {
		name     string
		prev     CPUSample
		cur      CPUSample
		expected uint64
	}{
		{
			name: "normal case - 1 core",
			prev: CPUSample{
				Timestamp:            now,
				UsageCoreNanoSeconds: 0,
			},
			cur: CPUSample{
				Timestamp:            now.Add(time.Second),
				UsageCoreNanoSeconds: 1000000000,
			},
			expected: 1000000000, // 1 core
		},
		{
			name: "normal case - 2 cores",
			prev: CPUSample{
				Timestamp:            now,
				UsageCoreNanoSeconds: 0,
			},
			cur: CPUSample{
				Timestamp:            now.Add(time.Second),
				UsageCoreNanoSeconds: 2000000000,
			},
			expected: 2000000000, // 2 cores
		},
		{
			name: "zero time delta",
			prev: CPUSample{
				Timestamp:            now,
				UsageCoreNanoSeconds: 0,
			},
			cur: CPUSample{
				Timestamp:            now, // same timestamp
				UsageCoreNanoSeconds: 1000000000,
			},
			expected: 0,
		},
		{
			name: "negative time delta",
			prev: CPUSample{
				Timestamp:            now.Add(time.Second),
				UsageCoreNanoSeconds: 0,
			},
			cur: CPUSample{
				Timestamp:            now, // earlier than prev
				UsageCoreNanoSeconds: 1000000000,
			},
			expected: 0,
		},
		{
			name: "CPU usage decreased (container restart)",
			prev: CPUSample{
				Timestamp:            now,
				UsageCoreNanoSeconds: 2000000000,
			},
			cur: CPUSample{
				Timestamp:            now.Add(time.Second),
				UsageCoreNanoSeconds: 1000000000, // less than prev
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateUsageNanoCores(tt.prev, tt.cur)
			if result != tt.expected {
				t.Errorf("calculateUsageNanoCores() = %d, want %d", result, tt.expected)
			}
		})
	}
}
