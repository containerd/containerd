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

package hasher

import (
	"hash"

	sha256simd "github.com/minio/sha256-simd"
)

func NewSHA256() hash.Hash {
	// An equivalent of sha256-simd is expected to be merged in Go 1.20 (1.21?): https://go-review.googlesource.com/c/go/+/408795/1
	return sha256simd.New()
}
