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

// Package stackdump captures goroutine stack dumps for diagnostics.
//
// Callers wire up their own SIGUSR1 handling on unix. Windows has no
// user-defined signal, so this package also provides the named Win32 event that
// stands in for it, shared by the daemon and its shims so one tool can trigger a
// dump in any containerd process.
package stackdump

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Dump returns a snapshot of the stacks of every goroutine in this process.
func Dump() []byte {
	var (
		buf       []byte
		stackSize int
	)
	bufferLen := 16384
	// runtime.Stack truncates to the buffer it is given and reports how much it
	// wrote, so a full buffer means the dump may have been cut short.
	for stackSize == len(buf) {
		buf = make([]byte, bufferLen)
		stackSize = runtime.Stack(buf, true)
		bufferLen *= 2
	}
	return buf[:stackSize]
}

// WriteFile writes buf to "<prefix>.<pid>.stacks.log" in the system temp
// directory and returns the path it wrote.
//
// Stacks are worth reading precisely when a process's log output may not be, so
// the dump also goes where diagnostics collection can retrieve it.
//
// The file is owner-only because a dump exposes the internals of a process that
// usually runs as root. That is a unix guarantee: Go's file mode reaches Windows
// only as FILE_ATTRIBUTE_READONLY, and not even that for a writable mode, so
// there the file simply inherits the temp directory's ACL.
func WriteFile(prefix string, buf []byte) (string, error) {
	name := filepath.Join(os.TempDir(), fmt.Sprintf("%s.%d.stacks.log", prefix, os.Getpid()))

	// Write a fresh file and rename it into place rather than opening name
	// directly. The temp directory is shared and the name is predictable, so
	// opening it would follow a symlink planted there and truncate whatever it
	// points at, with the privileges of a process that usually runs as root.
	// CreateTemp opens with O_EXCL and mode 0600, and the rename replaces any
	// symlink sitting at name instead of writing through it.
	f, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() {
		f.Close()
		os.Remove(tmp) // no-op once the rename below has succeeded
	}()

	if _, err := f.Write(buf); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, name); err != nil {
		return "", err
	}
	return name, nil
}
