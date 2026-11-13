//go:build !windows

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

package local

import (
	"errors"
	"os"
	"syscall"
)

// removeAttempts covers a writer closing concurrently with Abort.
// Close writes a timestamp file into the ingest directory, and RemoveAll
// can observe that new file only at the final rmdir and return ENOTEMPTY.
const removeAttempts = 5

func removePath(path string) error {
	var err error
	for range removeAttempts {
		err = os.RemoveAll(path)
		if err == nil || !isDirNotEmpty(err) {
			return err
		}
	}
	return err
}

func isDirNotEmpty(err error) bool {
	// POSIX rmdir reports a non-empty directory as ENOTEMPTY or, on some
	// platforms, EEXIST.
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}

func readFileWithRetry(path string) ([]byte, error) {
	return os.ReadFile(path)
}
