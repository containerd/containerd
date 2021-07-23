// +build gofuzz

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

package fuzz

import (
	"bytes"
	"context"
	"io/ioutil"
	"os"

	fuzz "github.com/AdaLogics/go-fuzz-headers"

	"github.com/containerd/containerd/archive"
)

// FuzzApply implements a fuzzer that applies
// a fuzzed tar archive on a directory
func FuzzApply(data []byte) int {
	f := fuzz.NewConsumer(data)
	iters, err := f.GetInt()
	if err != nil {
		return 0
	}
	maxIters := 20
	tmpDir, err := ioutil.TempDir("", "prefix-test")
	if err != nil {
		return 0
	}
	defer os.RemoveAll(tmpDir)
	for i := 0; i < iters%maxIters; i++ {
		rBytes, err := f.TarBytes()
		if err != nil {
			return 0
		}
		r := bytes.NewReader(rBytes)
		_, _ = archive.Apply(context.Background(), tmpDir, r)
	}
	return 1
}
