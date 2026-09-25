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

import (
	"strings"
	"testing"
)

// dial_addr is not supported on Windows; see hosts_dialaddr_windows.go. These
// tests pin that contract so the rejection cannot be lost silently.
func TestParseDialAddrRejectedOnWindows(t *testing.T) {
	cases := []struct {
		description string
		value       string
		expectedErr string
	}{
		{
			description: "pathname socket",
			value:       "unix:///run/registry-cache.sock",
			expectedErr: "not supported on Windows",
		},
		{
			description: "drive-letter URL form",
			value:       "unix:///C:/ProgramData/reg.sock",
			expectedErr: "not supported on Windows",
		},
		{
			description: "abstract socket",
			value:       "unix://@registry-cache",
			expectedErr: "not supported on Windows",
		},
	}

	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			// Direct parser call.
			if _, err := parseDialAddr(tc.value); err == nil || !strings.Contains(err.Error(), tc.expectedErr) {
				t.Fatalf("parseDialAddr(%q): want error containing %q, got %v", tc.value, tc.expectedErr, err)
			}
			// And through the hosts.toml config path.
			toml := "[host.\"http://example.registry\"]\n  dial_addr = \"" + tc.value + "\"\n"
			if _, err := parseHostsFile("", []byte(toml)); err == nil || !strings.Contains(err.Error(), tc.expectedErr) {
				t.Fatalf("parseHostsFile with dial_addr=%q: want error containing %q, got %v", tc.value, tc.expectedErr, err)
			}
		})
	}
}
