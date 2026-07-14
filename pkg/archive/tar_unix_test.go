//go:build !windows

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

package archive

import (
	"archive/tar"
	"errors"
	"math"
	"path/filepath"
	"testing"
)

func TestHandleTarTypeBlockCharFifoDeviceRange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		devmajor int64
		devminor int64
	}{
		{"major above uint32", math.MaxUint32 + 1, 0},
		{"minor above uint32", 0, math.MaxUint32 + 1},
		{"negative major", -1, 0},
		{"negative minor", 0, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hdr := &tar.Header{
				Typeflag: tar.TypeBlock,
				Devmajor: tc.devmajor,
				Devminor: tc.devminor,
			}
			err := handleTarTypeBlockCharFifo(hdr, filepath.Join(t.TempDir(), "dev"))
			if !errors.Is(err, errInvalidArchive) {
				t.Fatalf("expected errInvalidArchive for %d:%d, got %v", tc.devmajor, tc.devminor, err)
			}
		})
	}
}
