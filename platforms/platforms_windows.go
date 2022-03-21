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
	"log"
	"sync"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/windows/registry"
)

// NewMatcher returns a Windows matcher that will match on osVersionPrefix and osUBR if
// the platform is Windows otherwise use the default matcher
func newDefaultMatcher(platform specs.Platform) Matcher {
	prefix := prefix(platform.OSVersion) // note: the UBR in OSVersion is
	return windowsmatcher{
		Platform:        platform,
		osVersionPrefix: prefix,
		osUBR:           GetHostWindowsUpdateBuildRevision(),
		defaultMatcher: &matcher{
			Platform: Normalize(platform),
		},
	}
}

var (
	ubr  int
	once sync.Once
)

func GetHostWindowsUpdateBuildRevision() int {
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
