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

package tracing

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

type stringer string

func (s stringer) String() string { return string(s) }

type nilStringer struct{}

func (*nilStringer) String() string { panic("should not panic") }

type nilError struct{}

func (*nilError) Error() string { panic("should not panic") }

func TestKeyValue(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want attribute.KeyValue
	}{
		{
			name: "nil",
			want: attribute.String("key", "<nil>"),
		},
		{
			name: "string",
			in:   "value",
			want: attribute.String("key", "value"),
		},
		{
			name: "error",
			in:   errors.New("error message"),
			want: attribute.String("key", "error message"),
		},
		{
			name: "typed nil error",
			in:   (*nilError)(nil),
			want: attribute.String("key", "<nil>"),
		},
		{
			name: "stringer",
			in:   stringer("string value"),
			want: attribute.String("key", "string value"),
		},
		{
			name: "typed nil stringer",
			in:   (*nilStringer)(nil),
			want: attribute.String("key", "<nil>"),
		},
		{
			name: "JSON",
			in:   struct{ Value string }{"foo"},
			want: attribute.String("key", `{"Value":"foo"}`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := keyValue("key", tc.in)
			if got != tc.want {
				t.Errorf("keyValue() = %v; want %v", got, tc.want)
			}
		})
	}
}
