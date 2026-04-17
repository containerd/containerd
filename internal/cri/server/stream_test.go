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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendInBatches(t *testing.T) {
	type item struct{ id int }

	makeItems := func(n int) []*item {
		items := make([]*item, n)
		for i := range items {
			items[i] = &item{id: i}
		}
		return items
	}

	for _, test := range []struct {
		desc      string
		numItems  int
		batchSize int
		// expected number of send calls
		expectBatches int
		// expected sizes of each batch
		expectBatchSizes []int
	}{
		{
			desc:             "empty items",
			numItems:         0,
			batchSize:        5,
			expectBatches:    0,
			expectBatchSizes: nil,
		},
		{
			desc:             "items less than batch size",
			numItems:         3,
			batchSize:        5,
			expectBatches:    1,
			expectBatchSizes: []int{3},
		},
		{
			desc:             "items equal to batch size",
			numItems:         5,
			batchSize:        5,
			expectBatches:    1,
			expectBatchSizes: []int{5},
		},
		{
			desc:             "items greater than batch size",
			numItems:         12,
			batchSize:        5,
			expectBatches:    3,
			expectBatchSizes: []int{5, 5, 2},
		},
		{
			desc:             "single item",
			numItems:         1,
			batchSize:        5,
			expectBatches:    1,
			expectBatchSizes: []int{1},
		},
		{
			desc:             "items exactly double batch size",
			numItems:         10,
			batchSize:        5,
			expectBatches:    2,
			expectBatchSizes: []int{5, 5},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			items := makeItems(test.numItems)
			var batches [][]*item
			err := sendInBatches(context.Background(), items, test.batchSize, func(batch []*item) error {
				batches = append(batches, batch)
				return nil
			})
			assert.NoError(t, err)
			assert.Len(t, batches, test.expectBatches)
			for i, expectedSize := range test.expectBatchSizes {
				assert.Len(t, batches[i], expectedSize, "batch %d", i)
			}
			// Verify all items are included in order
			var allItems []*item
			for _, batch := range batches {
				allItems = append(allItems, batch...)
			}
			if test.numItems == 0 {
				assert.Empty(t, allItems)
			} else {
				assert.Equal(t, items, allItems)
			}
		})
	}
}

func TestSendInBatchesContextCancelled(t *testing.T) {
	type item struct{ id int }
	items := make([]*item, 10)
	for i := range items {
		items[i] = &item{id: i}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	var sendCount int
	err := sendInBatches(ctx, items, 3, func(batch []*item) error {
		sendCount++
		return nil
	})
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, 0, sendCount, "should not send any batches when context is already cancelled")
}

func TestSendInBatchesSendError(t *testing.T) {
	type item struct{ id int }
	items := make([]*item, 10)
	for i := range items {
		items[i] = &item{id: i}
	}

	sendErr := fmt.Errorf("send failed")
	var sendCount int
	err := sendInBatches(context.Background(), items, 3, func(batch []*item) error {
		sendCount++
		if sendCount == 2 {
			return sendErr
		}
		return nil
	})
	assert.ErrorIs(t, err, sendErr)
	assert.Equal(t, 2, sendCount, "should stop after send error")
}

func TestSendInBatchesContextCancelledMidStream(t *testing.T) {
	type item struct{ id int }
	items := make([]*item, 10)
	for i := range items {
		items[i] = &item{id: i}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var sendCount int
	err := sendInBatches(ctx, items, 3, func(batch []*item) error {
		sendCount++
		if sendCount == 2 {
			cancel() // cancel after second batch
		}
		return nil
	})

	// Context cancellation is checked before each batch send, so:
	// - Batch 1 (items 0-2): sent ok
	// - Batch 2 (items 3-5): sent ok, then cancel() called
	// - Batch 3 (items 6-8): context check fails, returns error
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, 2, sendCount)
}
