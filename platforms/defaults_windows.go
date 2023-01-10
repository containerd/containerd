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
	"runtime"
	"strconv"
	"strings"

	"github.com/Microsoft/hcsshim/osversion"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// DefaultSpec returns the current platform's default platform specification.
func DefaultSpec() specs.Platform {
	// NOTE: `windows.RtlGetNtVersionNumbers()` fetches the real version numbers of the
	// underlying Windows OS, while `windows.GetVersion()` might return different results
	// based on the binary being manifested and/or being run in compatibility mode.
	// `hcsshim.osversion.Get()` uses the latter call, so we must explicitly call
	// `RtlGetNtVersionNumbers()` here ourselves.
	major, minor, build := windows.RtlGetNtVersionNumbers()
	return specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		OSVersion:    fmt.Sprintf("%d.%d.%d", major, minor, build),
		// The Variant field will be empty if arch != ARM.
		Variant: cpuVariant(),
	}
}

// defaultWindowsMatcher is the default Matcher for Windows platforms.
//
// On pre-RS5 hosts, the default matcher compares a given platforms' build number so it exactly
// matches the expected build number, alongside the default OS type/architecture checks.
//
// On hosts which are RS5+ and should be able to run any newer/older Windows images under
// Hyper-V isolation, only the OS type/architecture are checked.
//
// https://learn.microsoft.com/en-us/virtualization/windowscontainers/deploy-containers/version-compatibility#windows-server-host-os-compatibility
type defaultWindowsMatcher struct {
	innerPlatform  specs.Platform
	defaultMatcher Matcher
}

// Match matches platforms based on their Windows build numbers coinciding, alongside the
// default matcher's rules for matching OS type, CPU architecture, and CPU variant.
// If the matcher's reference platform's build number is greater or equal to RS5 (1809),
// the build number constraint is dropped, as any older/newer images should be usable
// under Hyper-V isolation regardless of the reference platform.
func (m defaultWindowsMatcher) Match(p specs.Platform) bool {
	match := m.defaultMatcher.Match(p)

	if match && m.innerPlatform.OS == "windows" {
		innerOsv, _, err := osVersionFromString(m.innerPlatform.OSVersion)
		if err != nil {
			logrus.Warnf("failed to parse inner Windows platform string %q for comparison: %s", m.innerPlatform.OSVersion, err)
		}

		if innerOsv != nil && innerOsv.Build >= osversion.RS5 {
			// On RS5+ hosts, it is possible to run newer/older Windows container images under
			// Hyper-V isolation without any added constraints:
			return true
		}

		osv, _, err := osVersionFromString(p.OSVersion)
		if err != nil {
			logrus.Warnf("failed to parse Windows platform string %q for comparison: %s", p.OSVersion, err)
		}

		// If any of the Windows version strings are unparseable, give benefit of the doubt:
		if innerOsv == nil || osv == nil {
			return true
		}

		return innerOsv.Build == osv.Build
	}

	return match
}

// Less sorts matched platforms in front of other platforms.
// For matched platforms, it puts the platform closest to the comparer's platform
// in front of lesser matches.
// If the build numbers happen to coincide, any revision specifier is also taken into account.
func (m defaultWindowsMatcher) Less(p1, p2 specs.Platform) bool {
	m1, m2 := m.Match(p1), m.Match(p2)

	if m1 && m2 {
		osv1, rev1, err := osVersionFromString(p1.OSVersion)
		if err != nil {
			logrus.Warnf("failed to parse Windows platform string %q for comparison: %s", p1.OSVersion, err)
			// Rank  p1 < p2. (thus presuming p2 is parse-able)
			return true
		}

		osv2, rev2, err := osVersionFromString(p2.OSVersion)
		if err != nil {
			logrus.Warnf("failed to parse Windows platform string %q for comparison: %s", p2.OSVersion, err)
			// Rank p1 > p2, considering p1 is parse-able and p2 isn't.
			return false
		}

		// Fetch host platform version:
		innerOsv, _, err := osVersionFromString(m.innerPlatform.OSVersion)
		if err != nil {
			logrus.Warnf("failed to parse inner Windows platform string %q for comparison: %s", m.innerPlatform.OSVersion, err)
		}

		delta1 := absDiff(innerOsv.Build, osv1.Build)
		delta2 := absDiff(innerOsv.Build, osv2.Build)

		// If builds diffs, order by revision number:
		if delta1 == delta2 {
			if rev1 == 0 {
				return true
			}
			return rev1 < rev2
		}

		return delta1 < delta2
	}

	return m1 && !m2
}

// osVersionFromString returns an `osversion.OSVersion` for the provided Windows version string.
// E.g: "10.1.234" => OSVersion { Major: 10, Minor: 1, Build: 234 }
func osVersionFromString(version string) (*osversion.OSVersion, uint32, error) {
	var revision uint32
	splitVersion := strings.Split(version, ".")
	numParts := len(splitVersion)
	// Major.Minor.Build[.Revision]
	if numParts < 3 || numParts > 4 {
		return nil, revision, fmt.Errorf("Windows version string must contain either 3 or 4 dot-separated integers, got %q", version)
	}

	// Major.Minor.Build = uint8.uint8.uint16
	majorU64, err := strconv.ParseUint(splitVersion[0], 10, 8)
	if err != nil {
		return nil, revision, fmt.Errorf("failed to parse major version number %q from version string %q: %s", splitVersion[0], version, err)
	}
	minorU64, err := strconv.ParseUint(splitVersion[1], 10, 8)
	if err != nil {
		return nil, revision, fmt.Errorf("failed to parse minor version number %q from version string %q: %s", splitVersion[1], version, err)
	}
	buildU64, err := strconv.ParseUint(splitVersion[2], 10, 16)
	if err != nil {
		return nil, revision, fmt.Errorf("failed to parse build version number %q from version string %q: %s", splitVersion[2], version, err)
	}

	if numParts == 4 && splitVersion[3] != "" {
		rev, err := strconv.ParseUint(splitVersion[3], 10, 32)
		if err != nil {
			return nil, revision, fmt.Errorf("failed to parse revision number %q from version string %q: %s", splitVersion[3], version, err)
		}
		revision = uint32(rev)
	}

	// Main version is represented by uint32 which is in "reverse" (Build ++ Minor ++ Major)
	var versionU64 = (buildU64 << 16) & (minorU64 << 8) & majorU64
	return &osversion.OSVersion{
		Version:      uint32(versionU64),
		MajorVersion: uint8(majorU64),
		MinorVersion: uint8(minorU64),
		Build:        uint16(buildU64),
	}, revision, nil
}

// Default returns the current platform's default platform specification.
func Default() MatchComparer {
	return Only(DefaultSpec())
}

func absDiff(v1 uint16, v2 uint16) uint16 {
	if v1 > v2 {
		return v1 - v2
	}
	return v2 - v1
}
