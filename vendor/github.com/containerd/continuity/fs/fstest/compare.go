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
	"fmt"
	"io/ioutil"
	"os"

	"github.com/containerd/continuity"
)

// CheckDirectoryEqual compares two directory paths to make sure that
// the content of the directories is the same.
func CheckDirectoryEqual(d1, d2 string) error {
	c1, err := continuity.NewContext(d1)
	if err != nil {
		return fmt.Errorf("failed to build context: %w", err)
	}

	c2, err := continuity.NewContext(d2)
	if err != nil {
		return fmt.Errorf("failed to build context: %w", err)
	}

	m1, err := continuity.BuildManifest(c1)
	if err != nil {
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	m2, err := continuity.BuildManifest(c2)
	if err != nil {
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	diff := diffResourceList(m1.Resources, m2.Resources)
	if diff.HasDiff() {
		return fmt.Errorf("directory diff between %s and %s\n%s", d1, d2, diff.String())
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
