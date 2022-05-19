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

package internal

import "crypto/tls"

// TLSConfig returns a new tls.Config.
func TLSConfig() *tls.Config {
	// TLS 1.0 and 1.1 are no longer supported by Firefox, Chrome and Edge.
	// We could upgrade our minimum version as well.
	// - https://hacks.mozilla.org/2020/02/its-the-boot-for-tls-1-0-and-tls-1-1/
	// - https://chromestatus.com/feature/5759116003770368
	// - https://docs.microsoft.com/en-us/lifecycle/announcements/transport-layer-security-1x-disablement
	//
	//nolint:gosec
	return &tls.Config{MinVersion: tls.VersionTLS10}
}
