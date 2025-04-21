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

package config

import (
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
)

func FuzzParseHostsFile(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		f := fuzz.NewConsumer(data)
		dir := t.TempDir()
		err := f.CreateFiles(dir)
		if err != nil {
			return
		}
		b, err := f.GetBytes()
		if err != nil {
			return
		}
		_, _ = parseHostsFile(dir, b)
	})
}
