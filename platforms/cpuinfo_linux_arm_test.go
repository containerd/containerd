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

package platforms

import (
	"bufio"
	"github.com/agiledragon/gomonkey/v2"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"runtime"
	"testing"
)

func TestCPUVariant_MonkeyNoCpuArchitecture_ForAarch64(t *testing.T) {
	if !isArmArch(runtime.GOARCH) {
		t.Skip("only relevant on linux/arm")
	}
	fackFile := &os.File{}
	patches := gomonkey.ApplyFunc(os.Open, func(name string) (*os.File, error) {
		return fackFile, nil
	})
	defer patches.Reset()
	patches.ApplyMethodFunc(fackFile, "Close", func() error {
		return nil
	})
	scan := bufio.NewScanner(fackFile)
	patches.ApplyFunc(bufio.NewScanner, func(r io.Reader) *bufio.Scanner {
		return scan
	})
	patches.ApplyMethodFunc(scan, "Scan", func() bool {
		return false
	})
	patches.ApplyFunc(unix.Uname, func(uname *unix.Utsname) error {
		// value is aarch64
		uname.Machine = [65]byte{97, 97, 114, 99, 104, 54, 52} //nolint
		return nil
	})

	p := getCPUVariant()
	if p != "v8" {
		t.Fatalf("could not get valid variant as expected: %v\n", "v8")
	}

}

func TestCPUVariant_MonkeyNoCpuArchitecture_ForArmV7L(t *testing.T) {
	if !isArmArch(runtime.GOARCH) {
		t.Skip("only relevant on linux/arm")
	}
	fackFile := &os.File{}
	patches := gomonkey.ApplyFunc(os.Open, func(name string) (*os.File, error) {
		return fackFile, nil
	})
	defer patches.Reset()
	patches.ApplyMethodFunc(fackFile, "Close", func() error {
		return nil
	})
	scan := bufio.NewScanner(fackFile)
	patches.ApplyFunc(bufio.NewScanner, func(r io.Reader) *bufio.Scanner {
		return scan
	})
	patches.ApplyMethodFunc(scan, "Scan", func() bool {
		return false
	})
	patches.ApplyFunc(unix.Uname, func(uname *unix.Utsname) error {
		// value is armv7l
		uname.Machine = [65]byte{97, 114, 109, 118, 55, 108} //nolint
		return nil
	})
	p := getCPUVariant()
	if p != "v7" {
		t.Fatalf("could not get valid variant as expected: %v\n", "v7")
	}
}

func TestCPUVariant_MonkeyNoCpuArchitecture_ForArmV6B(t *testing.T) {
	if !isArmArch(runtime.GOARCH) {
		t.Skip("only relevant on linux/arm")
	}
	fackFile := &os.File{}
	patches := gomonkey.ApplyFunc(os.Open, func(name string) (*os.File, error) {
		return fackFile, nil
	})
	defer patches.Reset()
	patches.ApplyMethodFunc(fackFile, "Close", func() error {
		return nil
	})
	scan := bufio.NewScanner(fackFile)
	patches.ApplyFunc(bufio.NewScanner, func(r io.Reader) *bufio.Scanner {
		return scan
	})
	patches.ApplyMethodFunc(scan, "Scan", func() bool {
		return false
	})
	patches.ApplyFunc(unix.Uname, func(uname *unix.Utsname) error {
		// value is armv6b
		uname.Machine = [65]byte{97, 114, 109, 118, 54, 98} //nolint
		return nil
	})
	p := getCPUVariant()
	if p != "v6" {
		t.Fatalf("could not get valid variant as expected: %v\n", "v6")
	}
}

func TestCPUVariant_MonkeyNoCpuArchitecture_ForArmV5teL(t *testing.T) {
	if !isArmArch(runtime.GOARCH) {
		t.Skip("only relevant on linux/arm")
	}
	fackFile := &os.File{}
	patches := gomonkey.ApplyFunc(os.Open, func(name string) (*os.File, error) {
		return fackFile, nil
	})
	defer patches.Reset()
	patches.ApplyMethodFunc(fackFile, "Close", func() error {
		return nil
	})
	scan := bufio.NewScanner(fackFile)
	patches.ApplyFunc(bufio.NewScanner, func(r io.Reader) *bufio.Scanner {
		return scan
	})
	patches.ApplyMethodFunc(scan, "Scan", func() bool {
		return false
	})
	patches.ApplyFunc(unix.Uname, func(uname *unix.Utsname) error {
		// value is armv5tel
		uname.Machine = [65]byte{97, 114, 109, 118, 53, 116, 101, 108} //nolint
		return nil
	})
	p := getCPUVariant()
	if p != "v5" {
		t.Fatalf("could not get valid variant as expected: %v\n", "v5")
	}
}
