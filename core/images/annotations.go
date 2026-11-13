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

package images

const (
	// AnnotationImageName is an annotation on a Descriptor in an index.json
	// containing the `Name` value as used by an `Image` struct
	AnnotationImageName = "io.containerd.image.name"

	// AnnotationManifestSubject is an annotation on a Descriptor that means
	// that current descriptor is a referrer to the subject manifest.
	// If descriptor in image.json has this annotation, it will not create
	// a new image.
	AnnotationManifestSubject = "io.containerd.manifest.subject"

	// AnnotationErofsUncompressedDigest is a layer descriptor annotation,
	// defined by the EROFS image layer format specification
	// (https://github.com/erofs/erofs-image-spec), carrying the digest of an
	// application/vnd.erofs+zstd layer's decompressed content. When present
	// it MUST be used as the layer's DiffID, taking precedence over any
	// corresponding entry in the image config's rootfs.diff_ids.
	AnnotationErofsUncompressedDigest = "org.erofs.uncompressed-digest"
)
