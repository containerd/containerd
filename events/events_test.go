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

package events

import (
	"testing"

	"github.com/containerd/containerd/protobuf/types"
)

func TestEnvelope_Field(t *testing.T) {
	e := &Envelope{
		Namespace: "my-namespace",
		Topic:     "my-topic",
		Event: &types.Any{
			TypeUrl: "foobar",
			Value:   []byte("hello world"),
		},
	}

	tests := []struct {
		name      string
		fieldpath []string
		want      bool
	}{
		{
			name:      "get namespace",
			fieldpath: []string{"namespace"},
			want:      true,
		},
		{
			name:      "get topic",
			fieldpath: []string{"topic"},
			want:      true,
		},
		{
			name:      "get event",
			fieldpath: []string{"event"},
			want:      false,
		},
		{
			name:      "get topic by multi filed",
			fieldpath: []string{"topic", "namespace"},
			want:      true,
		},
		{
			name:      "filed path is unknown",
			fieldpath: []string{"unknown"},
			want:      false,
		},
		{
			name:      "filed path is null",
			fieldpath: []string{},
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := e.Field(tt.fieldpath)
			if ok != tt.want {
				t.Errorf("Envelope.Field() got = %v, want %v", ok, tt.want)
			}
		})
	}
}
