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

package dialer

import "testing"

func TestDialAddressScheme(t *testing.T) {
	testcases := []struct {
		name    string
		address string
		want    string
	}{
		{name: "bare path gets unix", address: "/run/containerd/containerd.sock", want: "unix:///run/containerd/containerd.sock"},
		{name: "unix scheme idempotent", address: "unix:///run/containerd/containerd.sock", want: "unix:///run/containerd/containerd.sock"},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DialAddress(tc.address); got != tc.want {
				t.Errorf("DialAddress(%q) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}
