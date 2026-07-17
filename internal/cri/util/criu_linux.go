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

package util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	criu "github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/utils"
)

// CheckCriu verifies that CRIU is available in the shim's PATH and meets the
// minimum version required for checkpoint and restore.
func CheckCriu(shimPath string) error {
	path := resolveCriuPath(shimPath)
	if path == "" {
		return errors.New("criu binary not found in shim path or system PATH")
	}
	client := criu.MakeCriu()
	client.SetCriuPath(path)
	version, err := client.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to retrieve criu version: %w", err)
	}
	if version < utils.PodCriuVersion {
		return fmt.Errorf("checkpoint/restore requires at least CRIU %d, current version is %d", utils.PodCriuVersion, version)
	}
	return nil
}

func resolveCriuPath(customPath string) string {
	if customPath != "" {
		// This logic is Linux-specific. If CRIU is ever supported on other
		// operating systems, path lookup will need to respect that OS's
		// conventions.
		for _, dir := range filepath.SplitList(customPath) {
			if !filepath.IsAbs(dir) {
				continue
			}
			criuPath := filepath.Join(dir, "criu")
			if fi, err := os.Stat(criuPath); err == nil && fi.Mode().IsRegular() && fi.Mode()&0111 != 0 {
				return criuPath
			}
		}
		return ""
	}
	if criuPath, err := exec.LookPath("criu"); err == nil {
		if absPath, err := filepath.Abs(criuPath); err == nil {
			return absPath
		}
		return criuPath
	}
	return ""
}
