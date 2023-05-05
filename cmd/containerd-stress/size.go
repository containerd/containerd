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

package main

import (
	"os"

	"github.com/containerd/containerd/log"
)

const defaultPath = "/usr/local/bin/"

var binaries = []string{
	defaultPath + "ctr",
	defaultPath + "containerd",
	defaultPath + "containerd-shim",
	defaultPath + "containerd-shim-runc-v1",
	defaultPath + "containerd-shim-runc-v2",
}

// checkBinarySizes checks and reports the binary sizes for the containerd compiled binaries to prometheus
func checkBinarySizes() {
	for _, name := range binaries {
		fi, err := os.Stat(name)
		if err != nil {
			log.L.WithError(err).Error("stat binary")
			continue
		}

		if fi.IsDir() {
			log.L.Error(name, "is not a file")
			continue
		}

		binarySizeGauge.WithValues(name).Set(float64(fi.Size()))
	}
}
