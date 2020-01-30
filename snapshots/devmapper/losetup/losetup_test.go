// +build linux

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

package losetup

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/docker/go-units"
	"golang.org/x/sys/unix"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"

	"github.com/containerd/containerd/pkg/testutil"
)

func TestLosetup(t *testing.T) {
	testutil.RequiresRoot(t)

	var (
		imagePath   = createSparseImage(t)
		loopDevice1 string
		loopDevice2 string
	)

	defer func() {
		err := os.Remove(imagePath)
		assert.NilError(t, err)
	}()

	t.Run("AttachLoopDevice", func(t *testing.T) {
		dev1, err := AttachLoopDevice(imagePath)
		assert.NilError(t, err)
		assert.Assert(t, dev1 != "")

		dev2, err := AttachLoopDevice(imagePath)
		assert.NilError(t, err)
		assert.Assert(t, dev2 != dev1, "should attach different loop device")

		loopDevice1 = dev1
		loopDevice2 = dev2
	})

	t.Run("AttachEmptyLoopDevice", func(t *testing.T) {
		_, err := AttachLoopDevice("")
		assert.Assert(t, err != nil, "shouldn't attach empty path")
	})

	t.Run("FindAssociatedLoopDevices", func(t *testing.T) {
		devices, err := FindAssociatedLoopDevices(imagePath)
		assert.NilError(t, err)
		assert.Assert(t, is.Len(devices, 2), "unexpected number of attached devices")
		assert.Assert(t, is.Contains(devices, loopDevice1))
		assert.Assert(t, is.Contains(devices, loopDevice2))
	})

	t.Run("FindAssociatedLoopDevicesForInvalidImage", func(t *testing.T) {
		devices, err := FindAssociatedLoopDevices("")
		assert.NilError(t, err)
		assert.Assert(t, is.Len(devices, 0))
	})

	t.Run("DetachLoopDevice", func(t *testing.T) {
		err := DetachLoopDevice(loopDevice2)
		assert.NilError(t, err, "failed to detach %q", loopDevice2)
	})

	t.Run("DetachEmptyDevice", func(t *testing.T) {
		err := DetachLoopDevice("")
		assert.Assert(t, err != nil, "shouldn't detach empty path")
	})

	t.Run("RemoveLoopDevicesAssociatedWithImage", func(t *testing.T) {
		err := RemoveLoopDevicesAssociatedWithImage(imagePath)
		assert.NilError(t, err)

		devices, err := FindAssociatedLoopDevices(imagePath)
		assert.NilError(t, err)
		assert.Assert(t, is.Len(devices, 0))
	})

	t.Run("RemoveLoopDevicesAssociatedWithInvalidImage", func(t *testing.T) {
		err := RemoveLoopDevicesAssociatedWithImage("")
		assert.NilError(t, err)
	})

	t.Run("DetachInvalidDevice", func(t *testing.T) {
		err := DetachLoopDevice("/dev/loop_invalid_idx")
		assert.Equal(t, unix.ENOENT, err)
	})
}

func createSparseImage(t *testing.T) string {
	file, err := ioutil.TempFile("", "losetup-tests-")
	assert.NilError(t, err)

	size, err := units.RAMInBytes("16Mb")
	assert.NilError(t, err)

	err = file.Truncate(size)
	assert.NilError(t, err)

	err = file.Close()
	assert.NilError(t, err)

	return file.Name()
}
