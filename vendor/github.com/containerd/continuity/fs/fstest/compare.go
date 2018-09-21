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

package fstest

import (
	"io/ioutil"
	"os"

	"github.com/containerd/continuity"
	"github.com/pkg/errors"
)

// CheckDirectoryEqual compares two directory paths to make sure that
// the content of the directories is the same.
func CheckDirectoryEqual(d1, d2 string) error {
	c1, err := continuity.NewContext(d1)
	if err != nil {
		return errors.Wrap(err, "failed to build context")
	}

	c2, err := continuity.NewContext(d2)
	if err != nil {
		return errors.Wrap(err, "failed to build context")
	}

	m1, err := continuity.BuildManifest(c1)
	if err != nil {
		return errors.Wrap(err, "failed to build manifest")
	}

	m2, err := continuity.BuildManifest(c2)
	if err != nil {
		return errors.Wrap(err, "failed to build manifest")
	}

	diff := diffResourceList(m1.Resources, m2.Resources)
	if diff.HasDiff() {
		if len(diff.Deletions) != 0 {
			return errors.Errorf("directory diff between %s and %s\n%s", d1, d2, diff.String())
		}
		// TODO: Also skip Recycle Bin contents in Windows layers which is used to store deleted files in some cases
		for _, add := range diff.Additions {
			if ok, _ := metadataFiles[add.Path()]; !ok {
				return errors.Errorf("directory diff between %s and %s\n%s", d1, d2, diff.String())
			}
		}
	}

	return nil
}

// CheckDirectoryEqualWithApplier compares directory against applier
func CheckDirectoryEqualWithApplier(root string, a Applier) error {
	applied, err := ioutil.TempDir("", "fstest")
	if err != nil {
		return err
	}
	defer os.RemoveAll(applied)
	if err := a.Apply(applied); err != nil {
		return err
	}
	return CheckDirectoryEqual(applied, root)
}
