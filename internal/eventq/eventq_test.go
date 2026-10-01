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

package eventq

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSingleSubscriber(t *testing.T) {
	eq := New[int](time.Second, func(int) {})
	c := newCollector(eq)
	expected := []int{1, 2, 3, 4, 5}
	for _, i := range expected {
		eq.Send(i)
	}
	eq.Shutdown()
	assert.Equal(t, expected, c.Collected())
}

func TestMultiSubscriber(t *testing.T) {
	eq := New[int](time.Second, func(int) {})
	cs := make([]*collector, 10)
	for i := range cs {
		cs[i] = newCollector(eq)
	}
	expected := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	for _, i := range expected {
		eq.Send(i)
	}
	eq.Shutdown()
	for i := range cs {
		assert.Equal(t, expected, cs[i].Collected())
	}
}

func TestMissedEvents(t *testing.T) {
	eq := New[int](3600*time.Second, func(int) {})
	expected := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	for _, i := range expected[:2] {
		eq.Send(i)
	}
	c := newCollector(eq)
	for _, i := range expected[2:] {
		eq.Send(i)
	}

	eq.Shutdown()
	assert.Equal(t, expected, c.Collected())
}

func TestSubscribersDifferentTime(t *testing.T) {
	eq := New[int](time.Second, func(int) {})
	expected := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	c1 := newCollector(eq)
	for _, i := range expected[:2] {
		eq.Send(i)
	}
	c2 := newCollector(eq)
	for _, i := range expected[2:] {
		eq.Send(i)
	}

	eq.Shutdown()
	assert.Equal(t, expected, c1.Collected())
	assert.Equal(t, expected[2:], c2.Collected())
}

func TestDiscardedAfterTime(t *testing.T) {
	discarded := []int{}
	eq := New[int](time.Microsecond, func(i int) {
		discarded = append(discarded, i)
	})
	expected := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	for _, i := range expected[:2] {
		eq.Send(i)
	}
	time.Sleep(50 * time.Millisecond)
	c := newCollector(eq)
	for _, i := range expected[2:] {
		eq.Send(i)
	}

	eq.Shutdown()
	assert.Equal(t, expected[:2], discarded)
	assert.Equal(t, expected[2:], c.Collected())
}

func TestDiscardedAtShutdown(t *testing.T) {
	discarded := []int{}
	expected := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	done := make(chan struct{})
	eq := New[int](3600*time.Second, func(i int) {
		discarded = append(discarded, i)
		if len(discarded) == len(expected) {
			close(done)
		}
	})
	for _, i := range expected {
		eq.Send(i)
	}
	eq.Shutdown()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	assert.Equal(t, expected, discarded)
}

func TestSlowSubscriberEvicted(t *testing.T) {
	setSlowSubscriberTimeout(t, 50*time.Millisecond)

	eq := New[int](time.Second, func(int) {})
	stuckC, stuckCloser := eq.Subscribe()
	defer stuckCloser.Close()
	c := newCollector(eq)

	expected := make([]int, 250)
	for i := range expected {
		expected[i] = i
	}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for _, i := range expected {
			eq.Send(i)
		}
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("Send blocked on a subscriber that stopped reading")
	}
	eq.Shutdown()

	assert.Equal(t, expected, c.Collected())
	assert.Equal(t, expected[:100], drain(t, stuckC))
}

func TestSlowSubscriberEvictedDuringReplay(t *testing.T) {
	setSlowSubscriberTimeout(t, 50*time.Millisecond)

	eq := New[int](3600*time.Second, func(int) {})
	expected := make([]int, 150)
	for i := range expected {
		expected[i] = i
		eq.Send(i)
	}
	stuckC, stuckCloser := eq.Subscribe()
	defer stuckCloser.Close()
	time.Sleep(4 * slowSubscriberTimeout)

	assert.Equal(t, expected[:100], drain(t, stuckC))

	c := newCollector(eq)
	eq.Send(150)
	eq.Shutdown()
	assert.Equal(t, append(expected[100:], 150), c.Collected())
}

func setSlowSubscriberTimeout(t *testing.T, d time.Duration) {
	orig := slowSubscriberTimeout
	slowSubscriberTimeout = d
	t.Cleanup(func() { slowSubscriberTimeout = orig })
}

func drain(t *testing.T, c <-chan int) []int {
	var got []int
	timeout := time.After(5 * time.Second)
	for {
		select {
		case i, ok := <-c:
			if !ok {
				return got
			}
			got = append(got, i)
		case <-timeout:
			t.Fatal("evicted subscriber channel was not closed")
			return got
		}
	}
}

type collector struct {
	collected []int
	c         <-chan int
	done      chan struct{}
}

func newCollector(eq EventQueue[int]) *collector {
	eventC, closer := eq.Subscribe()
	c := &collector{
		c:    eventC,
		done: make(chan struct{}),
	}
	go func() {
		defer close(c.done)
		defer closer.Close()
		for i := range c.c {
			c.collected = append(c.collected, i)
		}
	}()

	return c
}

func (c *collector) Collected() []int {
	<-c.done
	return c.collected
}
