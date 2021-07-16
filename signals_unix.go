// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris

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

package containerd

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// SIGRTMIN is defined by libc and different libc implementations have different values.
// But containerd only uses 34, the value used by GNU libc + NPTL's SIGRTMIN,
// to be compatible with Docker Engine.
const SIGRTMIN = 34
const rtPrefix = "SIGRTMIN+"

// ParseSignal parses a given string into a syscall.Signal
// the rawSignal can be a string with "SIG" prefix,
// or a signal number in string format.
func ParseSignal(rawSignal string) (syscall.Signal, error) {
	s, err := strconv.Atoi(rawSignal)
	if err == nil {
		return syscall.Signal(s), nil
	}

	return signalNum(rawSignal)
}

func signalNum(rawSignal string) (syscall.Signal, error) {
	name := strings.ToUpper(rawSignal)
	signal := unix.SignalNum(name)
	if signal != 0 {
		return signal, nil
	}

	if strings.HasPrefix(name, rtPrefix) {
		i, err := strconv.Atoi(name[len(rtPrefix):])
		if err != nil {
			return -1, errors.Wrapf(err, "unknown signal %q", rawSignal)
		}

		return syscall.Signal(SIGRTMIN + i), nil
	}

	return -1, fmt.Errorf("unknown signal %q", rawSignal)
}
