// +build linux,!no_rawblock

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

package rawblock

import (
	"strings"
	"testing"
)

func TestConfigSetDefaults(t *testing.T) {
	c := &SnapshotterConfig{}
	c.setDefaults("foo")

	if c.RootPath != "foo" {
		t.Errorf("default config rootpath expect %s found %s", "foo", c.RootPath)
	}

	if c.SizeMB != defaultImageSizeMB {
		t.Errorf("default config image size expect %d found %d", defaultImageSizeMB, c.SizeMB)
	}

	if c.FsType != defaultFsType {
		t.Errorf("default config image fstype expect %s found %s", defaultFsType, c.FsType)
	}

	c.FsType = "xfs"
	c.RootPath = "bar"

	c.setDefaults("foo")

	if c.RootPath != "bar" {
		t.Errorf("default config rootpath expect %s found %s", "bar", c.RootPath)
	}

	if c.FsType != "xfs" {
		t.Errorf("default config image fstype expect %s found %s", "xfs", c.FsType)
	}

	if !strings.Contains(strings.Join(c.Options, ","), "nouuid") {
		t.Errorf("default config image mount option missing %s", "nouuid")
	}
}

func TestConfigValidate(t *testing.T) {
	c := &SnapshotterConfig{}

	err := c.validate()
	if err == nil {
		t.Errorf("empty snapshotter config should fail validation")
	}

	c.setDefaults("")
	err = c.validate()
	if err != nil {
		t.Fatal(err)
	}
}
