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

package seekable

import (
	"io"
)

// countingWriter tracks total bytes written to compute offsets in non-seekable streams.
//
// This is necessary because:
//   - content.Writer (the containerd content store writer) does not implement io.Seeker
//   - The seekable EROFS format requires recording byte offsets for the chunk table
//     and dm-verity skippable frames
//   - Without tracking writes, we would need to buffer the entire blob in memory
//     just to know where each section starts
//
// The Offset() method allows writeChunkTable and other functions to record the
// current position in the output stream for later use in annotations.
type countingWriter struct {
	w      io.Writer
	offset int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.offset += int64(n)
	return n, err
}

// Offset returns the current byte offset (total bytes written).
func (w *countingWriter) Offset() int64 {
	return w.offset
}
