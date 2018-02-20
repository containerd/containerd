// +build !windows

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

package containerd

import (
	"runtime"
)

const (
	defaultRoot    = "/var/lib/containerd-test"
	defaultState   = "/run/containerd-test"
	defaultAddress = "/run/containerd-test/containerd.sock"
)

var (
	testImage string
)

func init() {
	switch runtime.GOARCH {
	case "386":
		testImage = "docker.io/i386/alpine:latest"
	case "arm":
		testImage = "docker.io/arm32v6/alpine:latest"
	case "arm64":
		testImage = "docker.io/arm64v8/alpine:latest"
	case "ppc64le":
		testImage = "docker.io/ppc64le/alpine:latest"
	case "s390x":
		testImage = "docker.io/s390x/alpine:latest"
	default:
		testImage = "docker.io/library/alpine:latest"
	}
}
