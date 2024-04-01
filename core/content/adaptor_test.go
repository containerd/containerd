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

package content

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdaptInfo(t *testing.T) {
	tests := []struct {
		name        string
		info        Info
		fieldpath   []string
		wantValue   string
		wantPresent bool
	}{
		{
			"empty fieldpath",
			Info{},
			[]string{},
			"",
			false,
		},
		{
			"digest fieldpath",
			Info{
				Digest: "foo",
			},
			[]string{"digest"},
			"foo",
			true,
		},
		{
			"size fieldpath",
			Info{},
			[]string{"size"},
			"",
			false,
		},
		{
			"labels fieldpath",
			Info{
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			[]string{"labels", "foo"},
			"bar",
			true,
		},
		{
			"labels join fieldpath",
			Info{
				Labels: map[string]string{
					"foo.bar.qux": "quux",
				},
			},
			[]string{"labels", "foo", "bar", "qux"},
			"quux",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptInfo := AdaptInfo(tt.info)

			value, present := adaptInfo.Field(tt.fieldpath)

			assert.Equal(t, tt.wantValue, value)
			assert.Equal(t, tt.wantPresent, present)
		})
	}
}
