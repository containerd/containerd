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

package lvm

import (
	"testing"

	"gotest.tools/assert"
)

const (
	rootpath = "/tmp/test_root"
)

func TestValidateConfig(t *testing.T) {

	c := SnapConfig{}
	err := c.Validate("")
	assert.Error(t, err, "Need both vol_group and thin_pool to be set")

	expected := SnapConfig{
		VgName:    "test_vg",
		ThinPool:  "test_pool",
		ImageSize: "10G",
		FsType:    "xfs",
		RootPath:  "/mnt",
	}

	c.VgName = "test_vg"
	c.ThinPool = "test_pool"
	err = c.Validate("")
	assert.NilError(t, err)
	assert.Equal(t, c, expected)

	c = SnapConfig{
		VgName:   "test_vg",
		ThinPool: "test_pool",
	}

	expected = SnapConfig{
		VgName:    "test_vg",
		ThinPool:  "test_pool",
		ImageSize: "10G",
		FsType:    "xfs",
		RootPath:  rootpath,
	}

	err = c.Validate(rootpath)
	assert.NilError(t, err)
	assert.Equal(t, c, expected)
}
