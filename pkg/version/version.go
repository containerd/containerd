/*
Copyright 2017 The Kubernetes Authors.

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

package version

import (
	"fmt"

	"github.com/blang/semver"
)

// CRIContainerdVersion is the version of the cri-containerd.
var CRIContainerdVersion = "UNKNOWN"

func validateSemver(sv string) error {
	_, err := semver.Parse(sv)
	if err != nil {
		return fmt.Errorf("couldn't parse cri-containerd version %q: %v", sv, err)
	}
	return nil
}

// PrintVersion outputs the release version of cri-containerd
func PrintVersion() {
	err := validateSemver(CRIContainerdVersion)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(CRIContainerdVersion)
}
