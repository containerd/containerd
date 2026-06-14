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

import "context"

const defaultStreamBatchSize = 5000

// sendInBatches sends items in batches using the provided send function.
// It checks for context cancellation before sending each batch.
// Returns nil immediately if items is empty (gRPC closes the stream with EOF).
func sendInBatches[T any](ctx context.Context, items []*T, batchSize int, sendFn func([]*T) error) error {
	for i := 0; i < len(items); i += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(i+batchSize, len(items))
		if err := sendFn(items[i:end]); err != nil {
			return err
		}
	}
	return nil
}
