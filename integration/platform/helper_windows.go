//go:build windows

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

package platform

import (
	"sync"

	"github.com/Microsoft/hcsshim/osversion"
	"golang.org/x/sys/windows"
)

var (
	osv  osversion.OSVersion
	once sync.Once
)

// Temporarily used on windows to skip failing test on WS2025.
func SkipTestOnHost() bool {
	const (
		// Copied from https://github.com/microsoft/hcsshim/blob/9b2e94f544990ce7e8f3ccdb60f1a9abd7debe05/osversion/windowsbuilds.go#L88-L90
		// Windows Server 2025 build 26100
		// The Windows Server 2025 constant was added in Microsoft/hcsshim@v0.13.0.
		// Copied here to avoid updating the hcsshim dependency which requires breaking changes in the archive package.
		V25H1Server = 26100
		LTSC2025    = V25H1Server
	)

	// Copied from https://github.com/microsoft/hcsshim/commit/1e6fc28c2f57d666f91269fc82d7b66c7d0d9093 (added in v0.13.0)
	// Pre Microsoft/hcsshim@v0.13.0, the osversion.Get() function required the calling application to be
	// manifested to get the correct version information. This is not the case anymore.
	once.Do(func() {
		v := windows.RtlGetVersion()
		osv = osversion.OSVersion{}
		osv.MajorVersion = uint8(v.MajorVersion)
		osv.MinorVersion = uint8(v.MinorVersion)
		osv.Build = uint16(v.BuildNumber)
	})

	return osv.Build == LTSC2025
}
