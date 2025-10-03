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

package erofs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func createWritableImage(ctx context.Context, filename string, size int64, uuid string) error {
	if err := createEmptyFile(filename, size); err != nil {
		return fmt.Errorf("failed to create empty file: %w", err)
	}

	args := []string{"-q"}
	if uuid != "" {
		args = append(args, []string{"-U", uuid}...)
	}
	args = append(args, filename)
	// TODO: Pre-resolve this and pass it in
	cmd := exec.CommandContext(ctx, "mkfs.ext4", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkfs.ext4 failed: %s: %w", out, err)
	}
	return nil
}

func createEmptyFile(filename string, size int64) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(size)
}
