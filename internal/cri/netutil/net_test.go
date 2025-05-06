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

/*
Copyright 2018 The Kubernetes Authors.

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

package net

import (
	"net"
	"testing"
)

func TestParseCIDRs(t *testing.T) {
	testCases := []struct {
		cidrs         []string
		errString     string
		errorExpected bool
	}{
		{
			cidrs:         []string{},
			errString:     "should not return an error for an empty slice",
			errorExpected: false,
		},
		{
			cidrs:         []string{"10.0.0.0/8", "not-a-valid-cidr", "2000::/10"},
			errString:     "should return error for bad cidr",
			errorExpected: true,
		},
		{
			cidrs:         []string{"10.0.0.0/8", "2000::/10"},
			errString:     "should not return error for good  cidrs",
			errorExpected: false,
		},
	}

	for _, tc := range testCases {
		cidrs, err := ParseCIDRs(tc.cidrs)
		if tc.errorExpected {
			if err == nil {
				t.Errorf("%v", tc.errString)
			}
			continue
		}
		if err != nil {
			t.Errorf("%v error:%v", tc.errString, err)
		}

		// validate lengths
		if len(cidrs) != len(tc.cidrs) {
			t.Errorf("cidrs should be of the same lengths %v != %v", len(cidrs), len(tc.cidrs))
		}

	}
}

func TestParsePort(t *testing.T) {
	var tests = []struct {
		name          string
		port          string
		allowZero     bool
		expectedPort  int
		expectedError bool
	}{
		{
			name:         "valid port: 1",
			port:         "1",
			expectedPort: 1,
		},
		{
			name:         "valid port: 1234",
			port:         "1234",
			expectedPort: 1234,
		},
		{
			name:         "valid port: 65535",
			port:         "65535",
			expectedPort: 65535,
		},
		{
			name:          "invalid port: not a number",
			port:          "a",
			expectedError: true,
			allowZero:     false,
		},
		{
			name:          "invalid port: too small",
			port:          "0",
			expectedError: true,
		},
		{
			name:          "invalid port: negative",
			port:          "-10",
			expectedError: true,
		},
		{
			name:          "invalid port: too big",
			port:          "65536",
			expectedError: true,
		},
		{
			name:      "zero port: allowed",
			port:      "0",
			allowZero: true,
		},
		{
			name:          "zero port: not allowed",
			port:          "0",
			expectedError: true,
		},
	}

	for _, rt := range tests {
		t.Run(rt.name, func(t *testing.T) {
			actualPort, actualError := ParsePort(rt.port, rt.allowZero)

			if actualError != nil && !rt.expectedError {
				t.Errorf("%s unexpected failure: %v", rt.name, actualError)
				return
			}
			if actualError == nil && rt.expectedError {
				t.Errorf("%s passed when expected to fail", rt.name)
				return
			}
			if actualPort != rt.expectedPort {
				t.Errorf("%s returned wrong port: got %d, expected %d", rt.name, actualPort, rt.expectedPort)
			}
		})
	}
}

func TestRangeSize(t *testing.T) {
	testCases := []struct {
		name  string
		cidr  string
		addrs int64
	}{
		{
			name:  "supported IPv4 cidr",
			cidr:  "192.168.1.0/24",
			addrs: 256,
		},
		{
			name:  "unsupported IPv4 cidr",
			cidr:  "192.168.1.0/1",
			addrs: 0,
		},
		{
			name:  "unsupported IPv6 mask",
			cidr:  "2001:db8::/1",
			addrs: 0,
		},
	}

	for _, tc := range testCases {
		_, cidr, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Errorf("failed to parse cidr for test %s, unexpected error: '%s'", tc.name, err)
		}
		if size := RangeSize(cidr); size != tc.addrs {
			t.Errorf("test %s failed. %s should have a range size of %d, got %d",
				tc.name, tc.cidr, tc.addrs, size)
		}
	}
}

func TestGetIndexedIP(t *testing.T) {
	testCases := []struct {
		cidr        string
		index       int
		expectError bool
		expectedIP  string
	}{
		{
			cidr:        "192.168.1.0/24",
			index:       20,
			expectError: false,
			expectedIP:  "192.168.1.20",
		},
		{
			cidr:        "192.168.1.0/30",
			index:       10,
			expectError: true,
		},
		{
			cidr:        "192.168.1.0/24",
			index:       255,
			expectError: false,
			expectedIP:  "192.168.1.255",
		},
		{
			cidr:        "255.255.255.0/24",
			index:       256,
			expectError: true,
		},
		{
			cidr:        "fd:11:b2:be::/120",
			index:       20,
			expectError: false,
			expectedIP:  "fd:11:b2:be::14",
		},
		{
			cidr:        "fd:11:b2:be::/126",
			index:       10,
			expectError: true,
		},
		{
			cidr:        "fd:11:b2:be::/120",
			index:       255,
			expectError: false,
			expectedIP:  "fd:11:b2:be::ff",
		},
		{
			cidr:        "00:00:00:be::/120",
			index:       255,
			expectError: false,
			expectedIP:  "::be:0:0:0:ff",
		},
	}

	for _, tc := range testCases {
		_, subnet, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Errorf("failed to parse cidr %s, unexpected error: '%s'", tc.cidr, err)
		}

		ip, err := GetIndexedIP(subnet, tc.index)
		if err == nil && tc.expectError || err != nil && !tc.expectError {
			t.Errorf("expectedError is %v and err is %s", tc.expectError, err)
			continue
		}

		if err == nil {
			ipString := ip.String()
			if ipString != tc.expectedIP {
				t.Errorf("expected %s but instead got %s", tc.expectedIP, ipString)
			}
		}

	}
}
