//go:build linux

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

package apparmor

import (
	"os"
	"testing"
)

func FuzzLoadDefaultProfile(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		tmpDir := t.TempDir()
		f, err := os.CreateTemp(tmpDir, "fuzz_file")
		if err != nil {
			return
		}
		defer f.Close()
		_, err = f.Write(data)
		if err != nil {
			return
		}
		_ = LoadDefaultProfile("fuzz_file")
	})
}
