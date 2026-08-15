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

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLayerTypeErofs(t *testing.T) {
	cases := []struct {
		mt   string
		want bool
	}{
		{MediaTypeErofsLayer, true},
		{MediaTypeErofsLayer + "+zstd", true},
		{MediaTypeErofs, true},
		{MediaTypeErofs + "+zstd", true},
		{MediaTypeErofsChunkIndex, true},
		{"application/vnd.erofs.unknown", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IsLayerType(c.mt), c.mt)
	}
}

func TestIsSkippableLayerType(t *testing.T) {
	cases := []struct {
		mt   string
		want bool
	}{
		{MediaTypeErofsChunkIndex, true},
		{MediaTypeErofs, false},
		{MediaTypeErofs + "+zstd", false},
		{MediaTypeErofsLayer, false},
		{"application/vnd.oci.image.layer.v1.tar+gzip", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IsSkippableLayerType(c.mt), c.mt)
	}
}
