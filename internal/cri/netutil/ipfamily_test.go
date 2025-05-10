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
	"fmt"
	"net"
	"testing"
)

func TestDualStackIPs(t *testing.T) {
	testCases := []struct {
		ips            []string
		errMessage     string
		expectedResult bool
		expectError    bool
	}{
		{
			ips:            []string{"1.1.1.1"},
			errMessage:     "should fail because length is not at least 2",
			expectedResult: false,
			expectError:    false,
		},
		{
			ips:            []string{},
			errMessage:     "should fail because length is not at least 2",
			expectedResult: false,
			expectError:    false,
		},
		{
			ips:            []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"},
			errMessage:     "should fail because all are v4",
			expectedResult: false,
			expectError:    false,
		},
		{
			ips:            []string{"fd92:20ba:ca:34f7:ffff:ffff:ffff:ffff", "fd92:20ba:ca:34f7:ffff:ffff:ffff:fff0", "fd92:20ba:ca:34f7:ffff:ffff:ffff:fff1"},
			errMessage:     "should fail because all are v6",
			expectedResult: false,
			expectError:    false,
		},
		{
			ips:            []string{"1.1.1.1", "not-a-valid-ip"},
			errMessage:     "should fail because 2nd ip is invalid",
			expectedResult: false,
			expectError:    true,
		},
		{
			ips:            []string{"not-a-valid-ip", "fd92:20ba:ca:34f7:ffff:ffff:ffff:ffff"},
			errMessage:     "should fail because 1st ip is invalid",
			expectedResult: false,
			expectError:    true,
		},
		{
			ips:            []string{"1.1.1.1", "fd92:20ba:ca:34f7:ffff:ffff:ffff:ffff"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
		{
			ips:            []string{"fd92:20ba:ca:34f7:ffff:ffff:ffff:ffff", "1.1.1.1", "fd92:20ba:ca:34f7:ffff:ffff:ffff:fff0"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
		{
			ips:            []string{"1.1.1.1", "fd92:20ba:ca:34f7:ffff:ffff:ffff:ffff", "10.0.0.0"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
		{
			ips:            []string{"fd92:20ba:ca:34f7:ffff:ffff:ffff:ffff", "1.1.1.1"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
	}
	// for each test case, test the regular func and the string func
	for _, tc := range testCases {
		dualStack, err := IsDualStackIPStrings(tc.ips)
		if err == nil && tc.expectError {
			t.Errorf("%s", tc.errMessage)
			continue
		}
		if err != nil && !tc.expectError {
			t.Errorf("failed to run test case for %v, error: %v", tc.ips, err)
			continue
		}
		if dualStack != tc.expectedResult {
			t.Errorf("%v for %v", tc.errMessage, tc.ips)
		}
	}

	for _, tc := range testCases {
		ips := make([]net.IP, 0, len(tc.ips))
		for _, ip := range tc.ips {
			parsedIP := net.ParseIP(ip)
			ips = append(ips, parsedIP)
		}
		dualStack, err := IsDualStackIPs(ips)
		if err == nil && tc.expectError {
			t.Errorf("%s", tc.errMessage)
			continue
		}
		if err != nil && !tc.expectError {
			t.Errorf("failed to run test case for %v, error: %v", tc.ips, err)
			continue
		}
		if dualStack != tc.expectedResult {
			t.Errorf("%v for %v", tc.errMessage, tc.ips)
		}
	}
}

func TestDualStackCIDRs(t *testing.T) {
	testCases := []struct {
		cidrs          []string
		errMessage     string
		expectedResult bool
		expectError    bool
	}{
		{
			cidrs:          []string{"10.10.10.10/8"},
			errMessage:     "should fail because length is not at least 2",
			expectedResult: false,
			expectError:    false,
		},
		{
			cidrs:          []string{},
			errMessage:     "should fail because length is not at least 2",
			expectedResult: false,
			expectError:    false,
		},
		{
			cidrs:          []string{"10.10.10.10/8", "20.20.20.20/8", "30.30.30.30/8"},
			errMessage:     "should fail because all cidrs are v4",
			expectedResult: false,
			expectError:    false,
		},
		{
			cidrs:          []string{"2000::/10", "3000::/10"},
			errMessage:     "should fail because all cidrs are v6",
			expectedResult: false,
			expectError:    false,
		},
		{
			cidrs:          []string{"10.10.10.10/8", "not-a-valid-cidr"},
			errMessage:     "should fail because 2nd cidr is invalid",
			expectedResult: false,
			expectError:    true,
		},
		{
			cidrs:          []string{"not-a-valid-ip", "2000::/10"},
			errMessage:     "should fail because 1st cidr is invalid",
			expectedResult: false,
			expectError:    true,
		},
		{
			cidrs:          []string{"10.10.10.10/8", "2000::/10"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
		{
			cidrs:          []string{"2000::/10", "10.10.10.10/8"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
		{
			cidrs:          []string{"2000::/10", "10.10.10.10/8", "3000::/10"},
			errMessage:     "expected success, but found failure",
			expectedResult: true,
			expectError:    false,
		},
	}

	// for each test case, test the regular func and the string func
	for _, tc := range testCases {
		dualStack, err := IsDualStackCIDRStrings(tc.cidrs)
		if err == nil && tc.expectError {
			t.Errorf("%s", tc.errMessage)
			continue
		}
		if err != nil && !tc.expectError {
			t.Errorf("failed to run test case for %v, error: %v", tc.cidrs, err)
			continue
		}
		if dualStack != tc.expectedResult {
			t.Errorf("%v for %v", tc.errMessage, tc.cidrs)
		}
	}

	for _, tc := range testCases {
		cidrs := make([]*net.IPNet, 0, len(tc.cidrs))
		for _, cidr := range tc.cidrs {
			_, parsedCIDR, _ := net.ParseCIDR(cidr)
			cidrs = append(cidrs, parsedCIDR)
		}

		dualStack, err := IsDualStackCIDRs(cidrs)
		if err == nil && tc.expectError {
			t.Errorf("%s", tc.errMessage)
			continue
		}
		if err != nil && !tc.expectError {
			t.Errorf("failed to run test case for %v, error: %v", tc.cidrs, err)
			continue
		}
		if dualStack != tc.expectedResult {
			t.Errorf("%v for %v", tc.errMessage, tc.cidrs)
		}
	}
}

func TestIPFamilyOfString(t *testing.T) {
	testCases := []struct {
		desc   string
		ip     string
		family IPFamily
	}{
		{
			desc:   "IPv4 1",
			ip:     "127.0.0.1",
			family: IPv4,
		},
		{
			desc:   "IPv4 2",
			ip:     "192.168.0.0",
			family: IPv4,
		},
		{
			desc:   "IPv4 3",
			ip:     "1.2.3.4",
			family: IPv4,
		},
		//{ // TODO: mikebrow work with networking team to establish if this divert from golang is nec.
		//	desc:   "IPv4 with leading 0s is accepted",
		//	ip:     "001.002.003.004",
		//	family: IPv4,
		//},
		{
			desc:   "IPv4 encoded as IPv6 is IPv4",
			ip:     "::FFFF:1.2.3.4",
			family: IPv4,
		},
		{
			desc:   "IPv6 1",
			ip:     "::1",
			family: IPv6,
		},
		{
			desc:   "IPv6 2",
			ip:     "fd00::600d:f00d",
			family: IPv6,
		},
		{
			desc:   "IPv6 3",
			ip:     "2001:db8::5",
			family: IPv6,
		},
		{
			desc:   "IPv4 with out-of-range octets is not accepted",
			ip:     "1.2.3.400",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with out-of-range segment is not accepted",
			ip:     "2001:db8::10005",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 with empty octet is not accepted",
			ip:     "1.2..4",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with multiple empty segments is not accepted",
			ip:     "2001::db8::5",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 CIDR is not accepted",
			ip:     "1.2.3.4/32",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 CIDR is not accepted",
			ip:     "2001:db8::/64",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4:port is not accepted",
			ip:     "1.2.3.4:80",
			family: IPFamilyUnknown,
		},
		{
			desc:   "[IPv6] with brackets is not accepted",
			ip:     "[2001:db8::5]",
			family: IPFamilyUnknown,
		},
		{
			desc:   "[IPv6]:port is not accepted",
			ip:     "[2001:db8::5]:80",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6%zone is not accepted",
			ip:     "fe80::1234%eth0",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 with leading whitespace is not accepted",
			ip:     " 1.2.3.4",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 with trailing whitespace is not accepted",
			ip:     "1.2.3.4 ",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with leading whitespace is not accepted",
			ip:     " 2001:db8::5",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with trailing whitespace is not accepted",
			ip:     " 2001:db8::5",
			family: IPFamilyUnknown,
		},
		{
			desc:   "random unparseable string",
			ip:     "bad ip",
			family: IPFamilyUnknown,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			family := IPFamilyOfString(tc.ip)
			isIPv4 := IsIPv4String(tc.ip)
			isIPv6 := IsIPv6String(tc.ip)

			if family != tc.family {
				t.Errorf("Expect %q family %q, got %q", tc.ip, tc.family, family)
			}
			if isIPv4 != (tc.family == IPv4) {
				t.Errorf("Expect %q ipv4 %v, got %v", tc.ip, tc.family == IPv4, isIPv6)
			}
			if isIPv6 != (tc.family == IPv6) {
				t.Errorf("Expect %q ipv6 %v, got %v", tc.ip, tc.family == IPv6, isIPv6)
			}
		})
	}
}

// This is specifically for testing "the kinds of net.IP values returned by net.ParseCIDR()"
func mustParseCIDRIP(cidrstr string) net.IP {
	ip, _, err := net.ParseCIDR(cidrstr)
	if ip == nil {
		panic(fmt.Sprintf("bad test case: %q should have been parseable: %v", cidrstr, err))
	}
	return ip
}

// This is specifically for testing "the kinds of net.IP values returned by net.ParseCIDR()"
func mustParseCIDRBase(cidrstr string) net.IP {
	_, cidr, err := net.ParseCIDR(cidrstr)
	if cidr == nil {
		panic(fmt.Sprintf("bad test case: %q should have been parseable: %v", cidrstr, err))
	}
	return cidr.IP
}

// This is specifically for testing "the kinds of net.IP values returned by net.ParseCIDR()"
func mustParseCIDRMask(cidrstr string) net.IPMask {
	_, cidr, err := net.ParseCIDR(cidrstr)
	if cidr == nil {
		panic(fmt.Sprintf("bad test case: %q should have been parseable: %v", cidrstr, err))
	}
	return cidr.Mask
}

func TestIsIPFamilyOf(t *testing.T) {
	testCases := []struct {
		desc   string
		ip     net.IP
		family IPFamily
	}{
		{
			desc:   "IPv4 all-zeros",
			ip:     net.IPv4zero,
			family: IPv4,
		},
		{
			desc:   "IPv6 all-zeros",
			ip:     net.IPv6zero,
			family: IPv6,
		},
		{
			desc:   "IPv4 broadcast",
			ip:     net.IPv4bcast,
			family: IPv4,
		},
		{
			desc:   "IPv4 loopback",
			ip:     net.ParseIP("127.0.0.1"),
			family: IPv4,
		},
		{
			desc:   "IPv6 loopback",
			ip:     net.IPv6loopback,
			family: IPv6,
		},
		{
			desc:   "IPv4 1",
			ip:     net.ParseIP("10.20.40.40"),
			family: IPv4,
		},
		{
			desc:   "IPv4 2",
			ip:     net.ParseIP("172.17.3.0"),
			family: IPv4,
		},
		{
			desc:   "IPv4 encoded as IPv6 is IPv4",
			ip:     net.ParseIP("::FFFF:1.2.3.4"),
			family: IPv4,
		},
		{
			desc:   "IPv6 1",
			ip:     net.ParseIP("fd00::600d:f00d"),
			family: IPv6,
		},
		{
			desc:   "IPv6 2",
			ip:     net.ParseIP("2001:db8::5"),
			family: IPv6,
		},
		{
			desc:   "IP from IPv4 CIDR is IPv4",
			ip:     mustParseCIDRIP("192.168.1.1/24"),
			family: IPv4,
		},
		{
			desc:   "CIDR base IP from IPv4 CIDR is IPv4",
			ip:     mustParseCIDRBase("192.168.1.1/24"),
			family: IPv4,
		},
		{
			desc:   "CIDR mask from IPv4 CIDR is IPv4",
			ip:     net.IP(mustParseCIDRMask("192.168.1.1/24")),
			family: IPv4,
		},
		{
			desc:   "IP from IPv6 CIDR is IPv6",
			ip:     mustParseCIDRIP("2001:db8::5/64"),
			family: IPv6,
		},
		{
			desc:   "CIDR base IP from IPv6 CIDR is IPv6",
			ip:     mustParseCIDRBase("2001:db8::5/64"),
			family: IPv6,
		},
		{
			desc:   "CIDR mask from IPv6 CIDR is IPv6",
			ip:     net.IP(mustParseCIDRMask("2001:db8::5/64")),
			family: IPv6,
		},
		{
			desc:   "nil is accepted, but is neither IPv4 nor IPv6",
			ip:     nil,
			family: IPFamilyUnknown,
		},
		{
			desc:   "invalid empty binary net.IP",
			ip:     net.IP([]byte{}),
			family: IPFamilyUnknown,
		},
		{
			desc:   "invalid short binary net.IP",
			ip:     net.IP([]byte{1, 2, 3}),
			family: IPFamilyUnknown,
		},
		{
			desc:   "invalid medium-length binary net.IP",
			ip:     net.IP([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}),
			family: IPFamilyUnknown,
		},
		{
			desc:   "invalid long binary net.IP",
			ip:     net.IP([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}),
			family: IPFamilyUnknown,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			family := IPFamilyOf(tc.ip)
			isIPv4 := IsIPv4(tc.ip)
			isIPv6 := IsIPv6(tc.ip)

			if family != tc.family {
				t.Errorf("Expect family %q, got %q", tc.family, family)
			}
			if isIPv4 != (tc.family == IPv4) {
				t.Errorf("Expect ipv4 %v, got %v", tc.family == IPv4, isIPv6)
			}
			if isIPv6 != (tc.family == IPv6) {
				t.Errorf("Expect ipv6 %v, got %v", tc.family == IPv6, isIPv6)
			}
		})
	}
}

func TestIPFamilyOfCIDR(t *testing.T) {
	testCases := []struct {
		desc   string
		cidr   string
		family IPFamily
	}{
		{
			desc:   "IPv4 CIDR 1",
			cidr:   "10.0.0.0/8",
			family: IPv4,
		},
		{
			desc:   "IPv4 CIDR 2",
			cidr:   "192.168.0.0/16",
			family: IPv4,
		},
		{
			desc:   "IPv6 CIDR 1",
			cidr:   "::/1",
			family: IPv6,
		},
		{
			desc:   "IPv6 CIDR 2",
			cidr:   "2000::/10",
			family: IPv6,
		},
		{
			desc:   "IPv6 CIDR 3",
			cidr:   "2001:db8::/32",
			family: IPv6,
		},
		{
			desc:   "bad CIDR",
			cidr:   "foo",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 with out-of-range octets is not accepted",
			cidr:   "1.2.3.400/32",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with out-of-range segment is not accepted",
			cidr:   "2001:db8::10005/64",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 with out-of-range mask length is not accepted",
			cidr:   "1.2.3.4/64",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with out-of-range mask length is not accepted",
			cidr:   "2001:db8::5/192",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 with empty octet is not accepted",
			cidr:   "1.2..4/32",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 with multiple empty segments is not accepted",
			cidr:   "2001::db8::5/64",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 IP is not CIDR",
			cidr:   "192.168.0.0",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 IP is not CIDR",
			cidr:   "2001:db8::",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 CIDR with leading whitespace is not accepted",
			cidr:   " 1.2.3.4/32",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv4 CIDR with trailing whitespace is not accepted",
			cidr:   "1.2.3.4/32 ",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 CIDR with leading whitespace is not accepted",
			cidr:   " 2001:db8::5/64",
			family: IPFamilyUnknown,
		},
		{
			desc:   "IPv6 CIDR with trailing whitespace is not accepted",
			cidr:   " 2001:db8::5/64",
			family: IPFamilyUnknown,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			family := IPFamilyOfCIDRString(tc.cidr)
			isIPv4 := IsIPv4CIDRString(tc.cidr)
			isIPv6 := IsIPv6CIDRString(tc.cidr)

			if family != tc.family {
				t.Errorf("Expect family %v, got %v", tc.family, family)
			}
			if isIPv4 != (tc.family == IPv4) {
				t.Errorf("Expect %q ipv4 %v, got %v", tc.cidr, tc.family == IPv4, isIPv6)
			}
			if isIPv6 != (tc.family == IPv6) {
				t.Errorf("Expect %q ipv6 %v, got %v", tc.cidr, tc.family == IPv6, isIPv6)
			}

			_, parsed, _ := net.ParseCIDR(tc.cidr)
			familyParsed := IPFamilyOfCIDR(parsed)
			isIPv4Parsed := IsIPv4CIDR(parsed)
			isIPv6Parsed := IsIPv6CIDR(parsed)
			if familyParsed != family {
				t.Errorf("%q gives different results for IPFamilyOfCIDR (%v) and IPFamilyOfCIDRString (%v)", tc.cidr, familyParsed, family)
			}
			if isIPv4Parsed != isIPv4 {
				t.Errorf("%q gives different results for IsIPv4CIDR (%v) and IsIPv4CIDRString (%v)", tc.cidr, isIPv4Parsed, isIPv4)
			}
			if isIPv6Parsed != isIPv6 {
				t.Errorf("%q gives different results for IsIPv6CIDR (%v) and IsIPv6CIDRString (%v)", tc.cidr, isIPv6Parsed, isIPv6)
			}
		})
	}
}
