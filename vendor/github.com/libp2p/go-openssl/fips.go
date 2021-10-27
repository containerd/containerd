// Copyright (C) 2017. See AUTHORS.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package openssl

/*
#include <openssl/ssl.h>
*/
import "C"
import "runtime"

// FIPSModeSet enables a FIPS 140-2 validated mode of operation.
// https://wiki.openssl.org/index.php/FIPS_mode_set()
func FIPSModeSet(mode bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var r C.int
	if mode {
		r = C.FIPS_mode_set(1)
	} else {
		r = C.FIPS_mode_set(0)
	}
	if r != 1 {
		return errorFromErrorQueue()
	}
	return nil
}
