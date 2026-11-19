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

package grpc

import (
	"github.com/containerd/containerd/v2/internal/wintls"
	"github.com/containerd/log"
)

// tlsResource holds Windows-specific TLS resources for cleanup on Stop.
var tlsResource wintls.CertResource

// setTLSResource caches the resource for later cleanup.
func setTLSResource(r wintls.CertResource) { tlsResource = r }

// cleanupTLSResources releases any cached TLS resources; safe to call multiple times.
func cleanupTLSResources() {
	if tlsResource != nil {
		if err := tlsResource.Close(); err != nil {
			log.L.WithError(err).Error("failed to cleanup TLS resources")
		}
		tlsResource = nil
	}
}
