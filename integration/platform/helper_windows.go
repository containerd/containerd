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

import "github.com/Microsoft/hcsshim/osversion"

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
	return osversion.Build() == LTSC2025
}
