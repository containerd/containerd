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
	"sync"
	"testing"
	"time"
)

func TestActivityTrackerInterface(t *testing.T) {
	tracker := NewActivityTrackerWithClock(&mockClock{now: 1})
	if tracker == nil {
		t.Fatal("NewActivityTrackerWithClock returned nil")
	}
}

func TestTouch(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()
	tracker.TimeSinceLastActivity()
	clock.Advance(time.Nanosecond)

	d := tracker.TimeSinceLastActivity()
	if d == 0 {
		t.Error("TimeSinceLastActivity() should not be 0 after time advances")
	}
}

func TestTouchMultipleTimes(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()
	d1 := tracker.TimeSinceLastActivity()

	clock.Advance(time.Millisecond)
	tracker.Touch()
	clock.Advance(time.Millisecond)
	d2 := tracker.TimeSinceLastActivity()

	if d2 <= d1 {
		t.Fatalf("Expected d2 (%v) > d1 (%v) after second Touch() updates timestamp", d2, d1)
	}
}

func TestStalledWithinWindow(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()
	clock.Advance(10 * time.Millisecond)

	if tracker.Stalled(100 * time.Millisecond) {
		t.Error("Stalled() should return false when within window")
	}
}

func TestStalledPastWindow(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()
	clock.Advance(60 * time.Millisecond)

	if !tracker.Stalled(50 * time.Millisecond) {
		t.Error("Stalled() should return true when past window")
	}

	tracker.Touch()
	clock.Advance(40 * time.Millisecond)

	if tracker.Stalled(50 * time.Millisecond) {
		t.Error("Stalled() should return false after Touch resets the window")
	}
}

func TestStalledNeverTouched(t *testing.T) {
	clock := &mockClock{}
	tracker := NewActivityTrackerWithClock(clock)

	if tracker.Stalled(time.Second) {
		t.Error("Stalled() should return false when never touched (initial state)")
	}
}

func TestStalledZeroWindow(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()

	if tracker.Stalled(0) {
		t.Error("Stalled(0) should return false")
	}

	if tracker.Stalled(-time.Second) {
		t.Error("Stalled(negative) should return false")
	}
}

func TestTimeSinceLastActivity(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	if tracker.TimeSinceLastActivity() != 0 {
		t.Error("TimeSinceLastActivity() should be 0 before any Touch()")
	}

	tracker.Touch()
	clock.Advance(20 * time.Millisecond)

	duration := tracker.TimeSinceLastActivity()
	if duration < 20*time.Millisecond {
		t.Errorf("TimeSinceLastActivity() = %v, expected >= 20ms", duration)
	}
}

func TestTimeSinceLastActivityNeverTouched(t *testing.T) {
	clock := &mockClock{}
	tracker := NewActivityTrackerWithClock(clock)

	duration := tracker.TimeSinceLastActivity()
	if duration != 0 {
		t.Errorf("TimeSinceLastActivity() should be 0 when never touched, got %v", duration)
	}
}

func TestConcurrentTouch(t *testing.T) {
	verifier := NewActivityVerifier()
	tracker := &mockActivityTrackerWithVerifier{
		verifier:    verifier,
		stallReturn: false,
		window:      5 * time.Second,
	}
	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				tracker.Touch()
			}
		}()
	}

	wg.Wait()

	expectedTouches := int32(numGoroutines * 10)
	actualTouches := verifier.GetTouchCount()
	if actualTouches != expectedTouches {
		t.Errorf("Expected %d Touch() calls, got %d", expectedTouches, actualTouches)
	}
}

func TestConcurrentStalled(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)
	var wg sync.WaitGroup
	numGoroutines := 50

	tracker.Touch()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.Stalled(50 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
}

func TestConcurrentTimeSinceLastActivity(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)
	var wg sync.WaitGroup
	numGoroutines := 50

	tracker.Touch()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.TimeSinceLastActivity()
			}
		}()
	}

	wg.Wait()
}

func TestActivityTrackerMultipleTouches(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	for i := 0; i < 5; i++ {
		tracker.Touch()
		clock.Advance(time.Millisecond)
	}

	duration := tracker.TimeSinceLastActivity()
	if duration == 0 {
		t.Error("TimeSinceLastActivity() should not be 0 after multiple Touch() calls")
	}
}

func TestStalledWithLargeWindow(t *testing.T) {
	clock := &mockClock{now: 1}
	window := 24 * time.Hour
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()

	if tracker.Stalled(window) {
		t.Error("Stalled() should return false when well within a large window")
	}
}

func TestTimeSinceLastActivityAccuracy(t *testing.T) {
	clock := &mockClock{now: 1}
	tracker := NewActivityTrackerWithClock(clock)

	tracker.Touch()
	clock.Advance(time.Nanosecond)

	duration := tracker.TimeSinceLastActivity()
	if duration != time.Nanosecond {
		t.Errorf("TimeSinceLastActivity() returned %v, expected exactly 1ns", duration)
	}
}

func TestTimeSinceLastActivityNeverTouchedRealClock(t *testing.T) {
	tracker := NewActivityTracker(time.Second)

	duration := tracker.TimeSinceLastActivity()
	if duration != 0 {
		t.Errorf("TimeSinceLastActivity() should be 0 when never touched with real clock, got %v", duration)
	}
}
