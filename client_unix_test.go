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
	"testing"

	"github.com/containerd/containerd/platforms"
)

const (
	defaultRoot    = "/var/lib/containerd-test"
	defaultState   = "/run/containerd-test"
	defaultAddress = "/run/containerd-test/containerd.sock"
)

var (
	testImage    string
	shortCommand = withProcessArgs("true")
	longCommand  = withProcessArgs("/bin/sh", "-c", "while true; do sleep 1; done")
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

func TestImagePullSchema1WithEmptyLayers(t *testing.T) {
	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := testContext(t)
	defer cancel()

	schema1TestImageWithEmptyLayers := "gcr.io/google-containers/busybox@sha256:d8d3bc2c183ed2f9f10e7258f84971202325ee6011ba137112e01e30f206de67"
	_, err = client.Pull(ctx, schema1TestImageWithEmptyLayers, WithPlatform(platforms.DefaultString()), WithSchema1Conversion, WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}
}
