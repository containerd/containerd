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

//go:build !linux

package verity

import "errors"

// errDmNotSupported is returned by dm-ioctl functions on non-Linux platforms.
var errDmNotSupported = errors.New("verity: dm-verity activation requires Linux")

// Open is not supported on non-Linux platforms.
func Open(_ *Params, _, _, _ string, _ []byte, _ string, _ []string) (string, error) {
	return "", errDmNotSupported
}

// Close is not supported on non-Linux platforms.
func Close(_ string) error { return errDmNotSupported }

// Check is not supported on non-Linux platforms.
func Check(_ string, _ []byte) bool { return false }
