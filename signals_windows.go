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

	"golang.org/x/sys/windows"
)

const (
	linuxSigrtmin = 34
	linuxSigrtmax = 64
)

var signalMapWindows = map[string]syscall.Signal{
	"HUP":  syscall.Signal(windows.SIGHUP),
	"INT":  syscall.Signal(windows.SIGINT),
	"QUIT": syscall.Signal(windows.SIGQUIT),
	"ILL":  syscall.Signal(windows.SIGILL),
	"TRAP": syscall.Signal(windows.SIGTRAP),
	"ABRT": syscall.Signal(windows.SIGABRT),
	"BUS":  syscall.Signal(windows.SIGBUS),
	"FPE":  syscall.Signal(windows.SIGFPE),
	"KILL": syscall.Signal(windows.SIGKILL),
	"SEGV": syscall.Signal(windows.SIGSEGV),
	"PIPE": syscall.Signal(windows.SIGPIPE),
	"ALRM": syscall.Signal(windows.SIGALRM),
	"TERM": syscall.Signal(windows.SIGTERM),
}

// manually define signals for linux since we may be running an LCOW container, but
// the unix syscalls do not get built when running on windows
var signalMapLinux = map[string]syscall.Signal{
	"ABRT":     syscall.Signal(0x6),
	"ALRM":     syscall.Signal(0xe),
	"BUS":      syscall.Signal(0x7),
	"CHLD":     syscall.Signal(0x11),
	"CLD":      syscall.Signal(0x11),
	"CONT":     syscall.Signal(0x12),
	"FPE":      syscall.Signal(0x8),
	"HUP":      syscall.Signal(0x1),
	"ILL":      syscall.Signal(0x4),
	"INT":      syscall.Signal(0x2),
	"IO":       syscall.Signal(0x1d),
	"IOT":      syscall.Signal(0x6),
	"KILL":     syscall.Signal(0x9),
	"PIPE":     syscall.Signal(0xd),
	"POLL":     syscall.Signal(0x1d),
	"PROF":     syscall.Signal(0x1b),
	"PWR":      syscall.Signal(0x1e),
	"QUIT":     syscall.Signal(0x3),
	"SEGV":     syscall.Signal(0xb),
	"STKFLT":   syscall.Signal(0x10),
	"STOP":     syscall.Signal(0x13),
	"SYS":      syscall.Signal(0x1f),
	"TERM":     syscall.Signal(0xf),
	"TRAP":     syscall.Signal(0x5),
	"TSTP":     syscall.Signal(0x14),
	"TTIN":     syscall.Signal(0x15),
	"TTOU":     syscall.Signal(0x16),
	"URG":      syscall.Signal(0x17),
	"USR1":     syscall.Signal(0xa),
	"USR2":     syscall.Signal(0xc),
	"VTALRM":   syscall.Signal(0x1a),
	"WINCH":    syscall.Signal(0x1c),
	"XCPU":     syscall.Signal(0x18),
	"XFSZ":     syscall.Signal(0x19),
	"RTMIN":    linuxSigrtmin,
	"RTMIN+1":  linuxSigrtmin + 1,
	"RTMIN+2":  linuxSigrtmin + 2,
	"RTMIN+3":  linuxSigrtmin + 3,
	"RTMIN+4":  linuxSigrtmin + 4,
	"RTMIN+5":  linuxSigrtmin + 5,
	"RTMIN+6":  linuxSigrtmin + 6,
	"RTMIN+7":  linuxSigrtmin + 7,
	"RTMIN+8":  linuxSigrtmin + 8,
	"RTMIN+9":  linuxSigrtmin + 9,
	"RTMIN+10": linuxSigrtmin + 10,
	"RTMIN+11": linuxSigrtmin + 11,
	"RTMIN+12": linuxSigrtmin + 12,
	"RTMIN+13": linuxSigrtmin + 13,
	"RTMIN+14": linuxSigrtmin + 14,
	"RTMIN+15": linuxSigrtmin + 15,
	"RTMAX-14": linuxSigrtmax - 14,
	"RTMAX-13": linuxSigrtmax - 13,
	"RTMAX-12": linuxSigrtmax - 12,
	"RTMAX-11": linuxSigrtmax - 11,
	"RTMAX-10": linuxSigrtmax - 10,
	"RTMAX-9":  linuxSigrtmax - 9,
	"RTMAX-8":  linuxSigrtmax - 8,
	"RTMAX-7":  linuxSigrtmax - 7,
	"RTMAX-6":  linuxSigrtmax - 6,
	"RTMAX-5":  linuxSigrtmax - 5,
	"RTMAX-4":  linuxSigrtmax - 4,
	"RTMAX-3":  linuxSigrtmax - 3,
	"RTMAX-2":  linuxSigrtmax - 2,
	"RTMAX-1":  linuxSigrtmax - 1,
	"RTMAX":    linuxSigrtmax,
}

// ParseSignal parses a given string into a syscall.Signal
// the rawSignal can be a string with "SIG" prefix,
// or a signal number in string format.
func ParseSignal(rawSignal string) (syscall.Signal, error) {
	return parseSignalGeneric(rawSignal, "windows")
}

// ParsePlatformSignal parses a given string into a syscall.Signal based on
// the OS platform specified in `platform`.
func ParsePlatformSignal(rawSignal, platform string) (syscall.Signal, error) {
	return parseSignalGeneric(rawSignal, platform)
}

func parseSignalGeneric(rawSignal, platform string) (syscall.Signal, error) {
	signalMap := getSignalMapForPlatform(platform)
	s, err := strconv.Atoi(rawSignal)
	if err == nil {
		sig := syscall.Signal(s)
		if platform != "windows" {
			return sig, nil
		}
		// on windows, make sure we support this signal
		for _, msig := range signalMap {
			if sig == msig {
				return sig, nil
			}
		}
		return sig, fmt.Errorf("unknown signal %q", rawSignal)
	}
	signal, ok := signalMap[strings.TrimPrefix(strings.ToUpper(rawSignal), "SIG")]
	if !ok {
		return -1, fmt.Errorf("unknown signal %q", rawSignal)
	}
	return signal, nil
}

func getSignalMapForPlatform(platform string) map[string]syscall.Signal {
	if platform != "windows" {
		return signalMapLinux
	}
	return signalMapWindows
}
