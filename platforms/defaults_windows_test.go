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

package platforms

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/Microsoft/hcsshim/osversion"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/windows"
)

func TestDefault(t *testing.T) {
	major, minor, build := windows.RtlGetNtVersionNumbers()
	expected := imagespec.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		OSVersion:    fmt.Sprintf("%d.%d.%d", major, minor, build),
		Variant:      cpuVariant(),
	}
	p := DefaultSpec()
	if !reflect.DeepEqual(p, expected) {
		t.Fatalf("default platform not as expected: %#v != %#v", p, expected)
	}

	s := DefaultString()
	if s != Format(p) {
		t.Fatalf("default specifier should match formatted default spec: %v != %v", s, p)
	}
}

func TestDefaultMatchComparer(t *testing.T) {
	defaultMatcher := Default()

	for _, test := range []struct {
		platform imagespec.Platform
		match    bool
	}{
		{
			platform: DefaultSpec(),
			match:    true,
		},
		{
			platform: imagespec.Platform{
				OS:           "linux",
				Architecture: runtime.GOARCH,
			},
			match: false,
		},
	} {
		assert.Equal(t, test.match, defaultMatcher.Match(test.platform))
	}

}

// Tests whether the Windows matcher works as expected for Windows releases
// older than RS5, which always require that the Host build number exactly
// match the image OS build number.
func TestMatchComparerMatch_WCOW_PreRS5(t *testing.T) {
	// NOTE: major/minor version numbers are irrelevant, only build number is checked.
	// Mask the build number with a pre-RS5 build:
	build := osversion.RS5 - 1
	buildStr := fmt.Sprintf("10.11.%d", build)

	samePlatformSpec := DefaultSpec()
	samePlatformSpec.OSVersion = buildStr

	m := defaultWindowsMatcher{
		innerPlatform: samePlatformSpec,
		defaultMatcher: &matcher{
			Platform: Normalize(samePlatformSpec),
		},
	}

	for _, test := range []struct {
		platform imagespec.Platform
		match    bool
	}{
		{
			platform: samePlatformSpec,
			match:    true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    buildStr + ".1",
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    buildStr + ".2",
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// Try a build number smaller than the current one:
				OSVersion: fmt.Sprintf("10.0.%d.1", build-1),
			},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// Try a build number larger than the current one:
				OSVersion: fmt.Sprintf("10.0.%d.1", build+1)},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
			},
			// If there is no platform.OSVersion, we assume it can run:
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "linux",
			},
			match: false,
		},
	} {
		assert.Equal(t, test.match, m.Match(test.platform), "should match: %t, %s to %s", test.match, m.innerPlatform, test.platform)
	}
}

// Tests whether the Windows matcher works as expected for RS5 and above
// hosts, which should allow running any older/newer Windows version
// if using Hyper-V isolation.
func TestMatchComparerMatch_WCOW_RS5(t *testing.T) {
	// NOTE: major/minor version numbers are irrelevant, only build number is checked.
	// Mask the build number with an RS5:
	build := osversion.RS5
	buildStr := fmt.Sprintf("10.11.%d", build)

	samePlatformSpec := DefaultSpec()
	samePlatformSpec.OSVersion = buildStr

	m := defaultWindowsMatcher{
		innerPlatform: samePlatformSpec,
		defaultMatcher: &matcher{
			Platform: Normalize(samePlatformSpec),
		},
	}

	for _, test := range []struct {
		platform imagespec.Platform
		match    bool
	}{
		{
			platform: samePlatformSpec,
			match:    true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    buildStr + ".1",
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				OSVersion:    buildStr + ".2",
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// Try a build number smaller than the current one:
				OSVersion: fmt.Sprintf("10.0.%d.1", build-1),
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// Try a build number larger than the current one:
				OSVersion: fmt.Sprintf("10.0.%d.1", build+1)},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// No OSVersion set at all.
			},
			match: true,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "linux",
			},
			match: false,
		},
	} {
		assert.Equal(t, test.match, m.Match(test.platform), "should match: %t, %s to %s", test.match, m.innerPlatform, test.platform)
	}
}

func TestMatchComparerMatch_LCOW(t *testing.T) {
	m := defaultWindowsMatcher{
		innerPlatform: imagespec.Platform{
			OS:           "linux",
			Architecture: "amd64",
		},
		defaultMatcher: &matcher{
			Platform: Normalize(imagespec.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
			),
		},
	}
	for _, test := range []struct {
		platform imagespec.Platform
		match    bool
	}{
		{
			platform: DefaultSpec(),
			match:    false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
			},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// Use an nonexistent Windows build so we don't get a match. Ws2019's build is 17763.
				OSVersion: "10.0.17762",
			},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "windows",
				// Use an nonexistent Windows build so we don't get a match. Ws2019's build is 17763.
				OSVersion: "10.0.17762.1",
			},
			match: false,
		},
		{
			platform: imagespec.Platform{
				Architecture: "amd64",
				OS:           "linux",
			},
			match: true,
		},
	} {
		assert.Equal(t, test.match, m.Match(test.platform), "should match %b, %s to %s", test.match, m.innerPlatform, test.platform)
	}
}

func TestMatchComparerLessPreRS5(t *testing.T) {
	defaultSpec := DefaultSpec()

	// Mask the build number with something less than RS5.
	// Note that because the `Less()` method mixes matching logic with the comparison logic
	// (i.e. it compares only successful matches to the current host), comparing a matching
	// and a non-matching version behaves counterintuitively. (e.g. it'll state 10.0.17764
	// is larger than 10.0.17762 when running on a 17764, but will claim otherwise on a 17762)
	// Here we force an internal version much smaller than the ones provided in the testcases
	// to avoid mixing the matching and comparing logic together.
	build := osversion.RS5 / 2
	defaultSpec.OSVersion = fmt.Sprintf("10.11.%d", build)
	m := defaultWindowsMatcher{
		innerPlatform: defaultSpec,
		defaultMatcher: &matcher{
			Platform: defaultSpec,
		},
	}

	match0 := fmt.Sprintf("10.0.%d", build)
	match1 := fmt.Sprintf("10.0.%d.10", build)
	match2 := fmt.Sprintf("10.0.%d.20", build)
	match3 := fmt.Sprintf("10.0.%d.30", build)

	platforms := []imagespec.Platform{
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17764.1",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match0,
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.3",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match3,
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match1,
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17762.2",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match2,
		},
	}
	// NOTE: because `Less()` mixes matching and comparison logic together, the ordering
	// of any platforms whose build number does not coincide with the matcher's inner
	// build number is at the mercy of the particular sorting algorithm being used.
	// (i.e. comparing two non-host-matching platforms will yield `false` in both cases).
	// In this sense, we can only validate that the supported matches are correctly ordered
	// and come in front of any non-matches.
	rankedMatches := []imagespec.Platform{
		{
			Architecture: "amd64",
			OS:           "windows",
			// NOTE: having no OSVersion will lead to a match.
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match0,
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match1,
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match2,
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    match3,
		},
	}
	sort.SliceStable(platforms, func(i, j int) bool {
		return m.Less(platforms[i], platforms[j])
	})
	assert.Equal(t, rankedMatches, platforms[:len(rankedMatches)])
}

func TestMatchComparerLessPostRS5(t *testing.T) {
	defaultSpec := DefaultSpec()

	// NOTE: major/minor version numbers are irrelevant, only build number is checked.
	// Mask the build number with an RS5:
	build := osversion.RS5
	defaultSpec.OSVersion = fmt.Sprintf("10.11.%d", build)
	m := defaultWindowsMatcher{
		innerPlatform: defaultSpec,
		defaultMatcher: &matcher{
			Platform: defaultSpec,
		},
	}

	platforms := []imagespec.Platform{
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17764.1",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.3",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.2",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17762.2",
		},
	}
	expected := []imagespec.Platform{
		{
			Architecture: "amd64",
			OS:           "windows",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17762.2",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.2",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17763.3",
		},
		{
			Architecture: "amd64",
			OS:           "windows",
			OSVersion:    "10.0.17764.1",
		},
	}
	sort.SliceStable(platforms, func(i, j int) bool {
		return m.Less(platforms[i], platforms[j])
	})
	assert.Equal(t, expected, platforms)
}

func TestOsVersionFromString(t *testing.T) {
	for _, test := range []struct {
		input         string
		osv           osversion.OSVersion
		revision      uint32
		expectedError string
	}{
		{
			input: "1.2.3.4",
			osv: osversion.OSVersion{
				MajorVersion: 1,
				MinorVersion: 2,
				Build:        3,
			},
			revision:      4,
			expectedError: "",
		},
		{
			input: "1.2.3",
			osv: osversion.OSVersion{
				MajorVersion: 1,
				MinorVersion: 2,
				Build:        3,
			},
			revision:      0,
			expectedError: "",
		},
		{
			input: "1.2.3.",
			osv: osversion.OSVersion{
				MajorVersion: 1,
				MinorVersion: 2,
				Build:        3,
			},
			// NOTE: should ignore incomplete revision but still work
			revision:      0,
			expectedError: "",
		},
		{
			input:         "",
			expectedError: "Windows version string must contain either 3 or 4 dot-separated integers",
		},
		{
			input:         "1",
			expectedError: "Windows version string must contain either 3 or 4 dot-separated integers",
		},
		{
			input:         "1.",
			expectedError: "Windows version string must contain either 3 or 4 dot-separated integers",
		},
		{
			input:         "1..",
			expectedError: "failed to parse minor version number",
		},
		{
			input:         ".1.",
			expectedError: "failed to parse major version number",
		},
		{
			input:         "..1",
			expectedError: "failed to parse major version number",
		},
		{
			input:         "XYZ.2.3.4",
			expectedError: "failed to parse major version number",
		},
		{
			input:         "1.XYZ.3.4",
			expectedError: "failed to parse minor version number",
		},
		{
			input:         "1.2.XYZ.4",
			expectedError: "failed to parse build version number",
		},
		{
			input:         "1.2.3.XYZ",
			expectedError: "failed to parse revision number",
		},
	} {
		osv, rev, err := osVersionFromString(test.input)
		if test.expectedError == "" {
			assert.Equal(t, test.osv.MajorVersion, osv.MajorVersion)
			assert.Equal(t, test.osv.MinorVersion, osv.MinorVersion)
			assert.Equal(t, test.osv.Build, osv.Build)
			assert.Equal(t, test.revision, rev)
		} else {
			assert.NotNil(t, err)
			assert.ErrorContains(t, err, test.expectedError)
		}
	}
}
