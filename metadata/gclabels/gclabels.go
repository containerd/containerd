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

// Labels that affect garbage collection.
// Documented at https://github.com/containerd/containerd/blob/main/docs/garbage-collection.md
package gclabels

import "time"

const (
	LabelGCRoot       = "containerd.io/gc.root"         // Keep this object and anything it references.
	LabelGCRef        = "containerd.io/gc.ref"          // This resource references another resource.
	LabelGCRefSnap    = "containerd.io/gc.ref.snapshot" // This resource references an identified snapshot from a particular snapshotter.
	LabelGCRefContent = "containerd.io/gc.ref.content"  // This resource references a content blob identified by digest, with optional user defined label key.
	LabelGCExpire     = "containerd.io/gc.expire"       // This lease expires after the given time in RFC3339 format
	LabelGCFlat       = "containerd.io/gc.flat"         // Ignore label references of leased resources.
)

var (
	LabelGCRootBytes       = []byte(LabelGCRoot)       // Keep this object and anything it references.
	LabelGCRefBytes        = []byte(LabelGCRef)        // This resource references another resource.
	LabelGCRefSnapBytes    = []byte(LabelGCRefSnap)    // This resource references an identified snapshot from a particular snapshotter.
	LabelGCRefContentBytes = []byte(LabelGCRefContent) // This resource references a content blob identified by digest, with optional user defined label key.
	LabelGCExpireBytes     = []byte(LabelGCExpire)     // This lease expires after the given time in RFC3339 format
	LabelGCFlatBytes       = []byte(LabelGCFlat)       // Ignore label references of leased resources.
)

func TimestampNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func TimestampFuture(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339)
}
