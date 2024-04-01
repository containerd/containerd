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

// Copyright 2015 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package systemd

import (
	"os"
	"sync"
)

var (
	runningSystemd bool
	detectSystemd  sync.Once
)

// IsRunningSystemd checks whether the host was booted with systemd as its init
// system. This functions similarly to systemd's `sd_booted(3)`: internally, it
// checks whether /run/systemd/system/ exists and is a directory.
// https://github.com/coreos/go-systemd/blob/d843340ab4bd3815fda02e648f9b09ae2dc722a7/util/util.go#L68-L78
func IsRunningSystemd() bool {
	detectSystemd.Do(func() {
		fi, err := os.Lstat("/run/systemd/system")
		if err != nil {
			return
		}
		runningSystemd = fi.IsDir()
	})
	return runningSystemd
}
