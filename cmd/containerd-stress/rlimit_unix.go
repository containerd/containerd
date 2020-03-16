// +build !windows,!freebsd

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
	"syscall"
)

func setRlimit() error {
	rlimit := uint64(100000)
	if rlimit > 0 {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
			return err
		}
		if limit.Cur < rlimit {
			limit.Cur = rlimit
			if limit.Max < limit.Cur {
				limit.Max = limit.Cur
			}
			if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
				return err
			}
		}
	}
	return nil
}
