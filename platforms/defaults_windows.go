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
	"log"
	"runtime"
	"strconv"
	"strings"
	"sync"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// DefaultSpec returns the current platform's default platform specification.
func DefaultSpec() specs.Platform {
	major, minor, build := windows.RtlGetNtVersionNumbers()
	ubr := getHostWindowsUpdateBuildRevision()
	return specs.Platform{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		OSVersion:    fmt.Sprintf("%d.%d.%d.%d", major, minor, build, ubr),
		// The Variant field will be empty if arch != ARM.
		Variant: cpuVariant(),
	}
}

type windowsmatcher struct {
	specs.Platform
	osVersionPrefix string
	osUBR           int
	defaultMatcher  Matcher
}

// Match matches platform with the same windows major, minor
// and build version.
func (m windowsmatcher) Match(p specs.Platform) bool {
	match := m.defaultMatcher.Match(p)

	if match && m.OS == "windows" {
		return strings.HasPrefix(p.OSVersion, m.osVersionPrefix) && m.defaultMatcher.Match(p)
	}

	return match
}

// Less sorts matched platforms in front of other platforms.
// For matched platforms, it puts platforms with matching revision (UBR)
// number in front, followed by larger revision.
func (m windowsmatcher) Less(p1, p2 specs.Platform) bool {
	m1, m2 := m.Match(p1), m.Match(p2)
	if m1 && m2 {
		r1, r2 := revision(p1.OSVersion), revision(p2.OSVersion)
		mubr1, mubr2 := r1 == m.osUBR, r2 == m.osUBR
		if mubr1 || mubr2 {
			return mubr1 && !mubr2
		}
		return r1 > r2
	}
	return m1 && !m2
}

func revision(v string) int {
	parts := strings.Split(v, ".")
	if len(parts) < 4 {
		return 0
	}
	r, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0
	}
	return r
}

func prefix(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 4 {
		return v
	}
	return strings.Join(parts[0:3], ".")
}

// Default returns the current platform's default platform specification.
func Default() MatchComparer {
	return Only(DefaultSpec())
}

var (
	ubr  int
	once sync.Once
)

// there is no windows system call in golang yet to get host UBR, so we do our own.
func getHostWindowsUpdateBuildRevision() int {
	once.Do(func() {
		ubr = 0
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
		if err != nil {
			log.Printf("can't open windows host UBR registry path: %v", err)
			return
		}
		defer k.Close()
		d, _, err := k.GetIntegerValue("UBR")
		if err != nil {
			log.Printf("can't read windows host UBR: %v", err)
			return
		}
		ubr = int(d)
	})
	return ubr
}
