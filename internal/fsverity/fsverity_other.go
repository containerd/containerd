//go:build !linux

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

package fsverity

import "fmt"

func IsSupported(string) (bool, error) {
	return false, fmt.Errorf("fsverity is only supported on Linux systems")
}

func IsEnabled(string) (bool, error) {
	return false, fmt.Errorf("fsverity is only supported on Linux systems")
}

func Enable(string) error {
	return fmt.Errorf("fsverity is only supported on Linux systems")
}
