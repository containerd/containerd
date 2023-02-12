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

package sbserver

import (
	"testing"
	"time"

	eventtypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/typeurl/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	testingclock "k8s.io/utils/clock/testing"
)

// TestBackOff tests the logic of backOff struct.
func TestBackOff(t *testing.T) {
	testStartTime := time.Now()
	testClock := testingclock.NewFakeClock(testStartTime)
	inputQueues := map[string]*backOffQueue{
		"container1": {
			events: []interface{}{
				&eventtypes.TaskOOM{ContainerID: "container1"},
				&eventtypes.TaskOOM{ContainerID: "container1"},
			},
		},
		"container2": {
			events: []interface{}{
				&eventtypes.TaskOOM{ContainerID: "container2"},
				&eventtypes.TaskOOM{ContainerID: "container2"},
			},
		},
	}
	expectedQueues := map[string]*backOffQueue{
		"container2": {
			events: []interface{}{
				&eventtypes.TaskOOM{ContainerID: "container2"},
				&eventtypes.TaskOOM{ContainerID: "container2"},
			},
			expireTime: testClock.Now().Add(backOffInitDuration),
			duration:   backOffInitDuration,
			clock:      testClock,
		},
		"container1": {
			events: []interface{}{
				&eventtypes.TaskOOM{ContainerID: "container1"},
				&eventtypes.TaskOOM{ContainerID: "container1"},
			},
			expireTime: testClock.Now().Add(backOffInitDuration),
			duration:   backOffInitDuration,
			clock:      testClock,
		},
	}

	t.Logf("Should be able to backOff a event")
	actual := newBackOff()
	actual.clock = testClock
	for k, queue := range inputQueues {
		for _, event := range queue.events {
			actual.enBackOff(k, event)
		}
	}
	assert.Equal(t, actual.queuePool, expectedQueues)

	t.Logf("Should be able to check if the container is in backOff state")
	for k, queue := range inputQueues {
		for _, e := range queue.events {
			any, err := typeurl.MarshalAny(e)
			assert.NoError(t, err)
			key, _, err := convertEvent(any)
			assert.NoError(t, err)
			assert.Equal(t, k, key)
			assert.Equal(t, actual.isInBackOff(key), true)
		}
	}

	t.Logf("Should be able to check that a container isn't in backOff state")
	notExistKey := "containerNotExist"
	assert.Equal(t, actual.isInBackOff(notExistKey), false)

	t.Logf("No containers should be expired")
	assert.Empty(t, actual.getExpiredIDs())

	t.Logf("Should be able to get all keys which are expired for backOff")
	testClock.Sleep(backOffInitDuration)
	actKeyList := actual.getExpiredIDs()
	assert.Equal(t, len(inputQueues), len(actKeyList))
	for k := range inputQueues {
		assert.Contains(t, actKeyList, k)
	}

	t.Logf("Should be able to get out all backOff events")
	doneQueues := map[string]*backOffQueue{}
	for k := range inputQueues {
		actQueue := actual.deBackOff(k)
		doneQueues[k] = actQueue
		assert.True(t, cmp.Equal(actQueue.events, expectedQueues[k].events, protobuf.Compare))
	}

	t.Logf("Should not get out the event again after having got out the backOff event")
	for k := range inputQueues {
		var expect *backOffQueue
		actQueue := actual.deBackOff(k)
		assert.Equal(t, actQueue, expect)
	}

	t.Logf("Should be able to reBackOff")
	for k, queue := range doneQueues {
		failEventIndex := 1
		events := queue.events[failEventIndex:]
		actual.reBackOff(k, events, queue.duration)
		actQueue := actual.deBackOff(k)
		expQueue := &backOffQueue{
			events:     events,
			expireTime: testClock.Now().Add(2 * queue.duration),
			duration:   2 * queue.duration,
			clock:      testClock,
		}
		assert.Equal(t, actQueue, expQueue)
	}
}
