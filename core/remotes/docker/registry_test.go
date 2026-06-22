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

package docker

import "testing"

func TestHasCapability(t *testing.T) {
	var (
		pull = HostCapabilityPull
		rslv = HostCapabilityResolve
		push = HostCapabilityPush
		all  = pull | rslv | push
	)
	for i, tc := range []struct {
		c HostCapabilities
		t HostCapabilities
		e bool
	}{
		{all, pull, true},
		{all, pull | rslv, true},
		{all, pull | push, true},
		{all, all, true},
		{pull, all, false},
		{pull, push, false},
		{rslv, pull, false},
		{pull | rslv, push, false},
		{pull | rslv, rslv, true},
	} {
		if a := tc.c.Has(tc.t); a != tc.e {
			t.Fatalf("%d: failed, expected %t, got %t", i, tc.e, a)
		}
	}
}

func TestMatchLocalhost(t *testing.T) {
	for _, tc := range []struct {
		host  string
		match bool
	}{
		{"", false},
		{"127.1.1.1", true},
		{"127.0.0.1", true},
		{"127.256.0.1", false}, // test MatchLocalhost does not panic on invalid ip
		{"127.23.34.52", true},
		{"127.0.0.1:5000", true},
		{"registry.org", false},
		{"126.example.com", false},
		{"localhost", true},
		{"localhost:5000", true},
		{"[127:0:0:1]", false},
		{"[::1]", true},
		{"[::1]:", false},     // invalid ip
		{"127.0.1.1:", false}, // invalid ip
		{"[::1]:5000", true},
		{"::1", true},
	} {
		actual, _ := MatchLocalhost(tc.host)
		if actual != tc.match {
			if tc.match {
				t.Logf("Expected match for %s", tc.host)
			} else {
				t.Logf("Unexpected match for %s", tc.host)
			}
			t.Fail()
		}
	}
}
