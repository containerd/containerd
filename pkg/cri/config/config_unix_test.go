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

package config

import "testing"

func TestDefaultConfig(t *testing.T) {
	tests := []struct {
		name   string
		expect func(c PluginConfig) bool
		want   bool
	}{
		{
			name: "runc default enable SystemdCgroup",
			expect: func(c PluginConfig) bool {
				if r, ok := c.Runtimes["runc"]; ok {
					if v, ok := r.Options["SystemdCgroup"]; ok {
						b, ok := v.(bool)
						if !ok {
							return false
						}
						return b
					}
				}
				return false
			},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			got := test.expect(config)
			if test.want != got {
				t.Fatalf("want {%+v}, got {%+v}", test.want, got)
			}
		})
	}
}
