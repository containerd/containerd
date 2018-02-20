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

	"github.com/containerd/containerd/errdefs"
)

func TestImageIsUnpacked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}

	const imageName = "docker.io/library/busybox:latest"
	ctx, cancel := testContext()
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Cleanup
	err = client.ImageService().Delete(ctx, imageName)
	if err != nil && !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}

	// By default pull does not unpack an image
	image, err := client.Pull(ctx, imageName)
	if err != nil {
		t.Fatal(err)
	}

	// Check that image is not unpacked
	unpacked, err := image.IsUnpacked(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	if unpacked {
		t.Fatalf("image should not be unpacked")
	}

	// Check that image is unpacked
	err = image.Unpack(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	unpacked, err = image.IsUnpacked(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
	if !unpacked {
		t.Fatalf("image should be unpacked")
	}
}
