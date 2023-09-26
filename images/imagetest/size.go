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

package imagetest

// Size represents the size of an object from different methods of counting, in bytes
type Size struct {
	// Manifest is the total Manifest reported size of current object and children
	Manifest int64

	// Content is the total of store Content of current object and children
	Content int64

	// Unpacked is the total size of Unpacked data for the given snapshotter
	// The key is the snapshotter name
	// The value is the usage reported by the snapshotter for the image
	Unpacked map[string]int64

	// Uncompressed is the total Uncompressed tar stream data (used for size estimate)
	Uncompressed int64
}

// ContentSizeCalculator calculates a size property for the test content
type ContentSizeCalculator func(Content) int64

// SizeOfManifest recursively calculates the manifest reported size,
// not accounting for duplicate entries.
func SizeOfManifest(t Content) int64 {
	accumulated := t.Size.Manifest
	for _, child := range t.Children {
		accumulated = accumulated + SizeOfManifest(child)
	}
	return accumulated
}

// SizeOfContent recursively calculates the size of all stored content
// The calculation is a simple accumulation based on the structure,
// duplicate blobs are not accounted for.
func SizeOfContent(t Content) int64 {
	accumulated := t.Size.Content
	for _, child := range t.Children {
		accumulated = accumulated + SizeOfContent(child)
	}
	return accumulated
}
