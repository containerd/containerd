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
	"sort"
	"sync"
	"time"
)

// CPUSample holds a single CPU usage sample with timestamp.
type CPUSample struct {
	Timestamp            time.Time
	UsageCoreNanoSeconds uint64
	// UsageNanoCores is the instantaneous CPU usage rate calculated
	// from this sample and the previous sample.
	UsageNanoCores uint64
}

// timedStoreDataSlice is a time-ordered slice of CPU samples.
type timedStoreDataSlice []CPUSample

func (t timedStoreDataSlice) Less(i, j int) bool {
	return t[i].Timestamp.Before(t[j].Timestamp)
}

func (t timedStoreDataSlice) Len() int {
	return len(t)
}

func (t timedStoreDataSlice) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

// TimedStore is a time-based buffer for CPU stats samples.
// It stores samples for a specific time period and/or a max number of items,
// similar to cAdvisor's TimedStore but specialized for CPU stats.
// All methods are thread-safe.
type TimedStore struct {
	mu       sync.RWMutex
	buffer   timedStoreDataSlice
	age      time.Duration
	maxItems int
}

// NewTimedStore creates a new TimedStore.
// age specifies how long samples are retained.
// maxItems specifies the maximum number of samples to keep (-1 for no limit).
func NewTimedStore(age time.Duration, maxItems int) *TimedStore {
	capacity := max(maxItems, 0) // Will grow dynamically
	return &TimedStore{
		buffer:   make(timedStoreDataSlice, 0, capacity),
		age:      age,
		maxItems: maxItems,
	}
}

// Add adds a new CPU sample to the store.
// It calculates UsageNanoCores from the previous sample if available.
// Samples are stored in timestamp order, and old samples are evicted
// based on age and maxItems constraints.
func (s *TimedStore) Add(timestamp time.Time, usageCoreNanoSeconds uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sample := CPUSample{
		Timestamp:            timestamp,
		UsageCoreNanoSeconds: usageCoreNanoSeconds,
	}

	// Calculate UsageNanoCores from the previous sample if we have one
	if len(s.buffer) > 0 {
		lastSample := s.buffer[len(s.buffer)-1]
		sample.UsageNanoCores = calculateUsageNanoCores(lastSample, sample)
	}

	// Common case: data is added in order
	if len(s.buffer) == 0 || !timestamp.Before(s.buffer[len(s.buffer)-1].Timestamp) {
		s.buffer = append(s.buffer, sample)
	} else {
		// Data is out of order; insert it in the correct position
		index := sort.Search(len(s.buffer), func(index int) bool {
			return s.buffer[index].Timestamp.After(timestamp)
		})
		s.buffer = append(s.buffer, CPUSample{}) // Make room
		copy(s.buffer[index+1:], s.buffer[index:])
		s.buffer[index] = sample
		// Recalculate UsageNanoCores for this and the next sample
		if index > 0 {
			s.buffer[index].UsageNanoCores = calculateUsageNanoCores(s.buffer[index-1], s.buffer[index])
		}
		if index+1 < len(s.buffer) {
			s.buffer[index+1].UsageNanoCores = calculateUsageNanoCores(s.buffer[index], s.buffer[index+1])
		}
	}

	// Remove any elements before eviction time
	evictTime := timestamp.Add(-s.age)
	index := sort.Search(len(s.buffer), func(index int) bool {
		return s.buffer[index].Timestamp.After(evictTime)
	})
	if index < len(s.buffer) && index > 0 {
		s.buffer = s.buffer[index:]
	}

	// Remove any elements if over max size
	if s.maxItems >= 0 && len(s.buffer) > s.maxItems {
		startIndex := len(s.buffer) - s.maxItems
		s.buffer = s.buffer[startIndex:]
	}
}

// GetLatest returns the most recent sample, or nil if no samples exist.
func (s *TimedStore) GetLatest() *CPUSample {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.buffer) == 0 {
		return nil
	}
	sample := s.buffer[len(s.buffer)-1]
	return &sample
}

// usageRateWindow is how far back UsageNanoCores looks when reporting a rate.
// Samples are taken every second, but kubelet's cAdvisor housekeeping and the
// node conformance summary API both observe CPU over a multi-second interval
// (the summary check repeats for 30s). Averaging only the last second reports
// 0 as soon as a container goes briefly idle, which the conformance test
// rejects even though the container has used CPU inside this window.
const usageRateWindow = 30 * time.Second

// GetLatestUsageNanoCores returns the CPU usage rate over usageRateWindow
// (or the full buffered span, when that is shorter).
// Returns 0 and false if fewer than two samples exist.
func (s *TimedStore) GetLatestUsageNanoCores() (uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n := len(s.buffer)
	if n < 2 {
		return 0, false
	}

	latest := s.buffer[n-1]
	cutoff := latest.Timestamp.Add(-usageRateWindow)
	baselineIdx := n - 2
	for i := n - 2; i >= 0; i-- {
		baselineIdx = i
		if !s.buffer[i].Timestamp.After(cutoff) {
			break
		}
	}
	baseline := s.buffer[baselineIdx]
	// A container restart resets the cumulative counter. Rate from the
	// pre-reset sample is not meaningful; start at the reset instead.
	if latest.UsageCoreNanoSeconds < baseline.UsageCoreNanoSeconds {
		for i := baselineIdx + 1; i < n-1; i++ {
			if s.buffer[i].UsageCoreNanoSeconds <= latest.UsageCoreNanoSeconds {
				baseline = s.buffer[i]
				break
			}
		}
	}
	if !baseline.Timestamp.Before(latest.Timestamp) {
		return 0, false
	}
	return calculateUsageNanoCores(baseline, latest), true
}

// Size returns the number of samples in the store.
func (s *TimedStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.buffer)
}

// calculateUsageNanoCores calculates the instantaneous CPU usage rate
// (nanocores) from two consecutive samples.
// Returns 0 if the calculation cannot be performed (e.g., invalid time delta).
func calculateUsageNanoCores(prev, cur CPUSample) uint64 {
	nanoSeconds := cur.Timestamp.UnixNano() - prev.Timestamp.UnixNano()

	// Zero or negative interval
	if nanoSeconds <= 0 {
		return 0
	}

	// CPU usage can't go backwards (might happen if container was restarted)
	if cur.UsageCoreNanoSeconds < prev.UsageCoreNanoSeconds {
		return 0
	}

	usageDelta := cur.UsageCoreNanoSeconds - prev.UsageCoreNanoSeconds
	return uint64(float64(usageDelta) / float64(nanoSeconds) * float64(time.Second/time.Nanosecond))
}
