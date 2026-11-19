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

package erofsutils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/containerd/log"
)

// The functions in this file build EROFS images with the mkfs.erofs tool from
// erofs-utils. They are used only when mkfs.erofs is selected explicitly; the
// Go converters in convert.go are the default.

func mkfsEnv(layerPath string) []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	env := os.Environ()
	tmpdir := filepath.Dir(layerPath)
	env = append(env, "TMPDIR="+tmpdir)
	return env
}

// MkfsSupportsTar reports whether the installed mkfs.erofs supports tar mode
// (the --tar option). It returns an error when mkfs.erofs cannot be run.
func MkfsSupportsTar() (bool, error) {
	cmd := exec.Command("mkfs.erofs", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to run mkfs.erofs --help: %w", err)
	}

	return bytes.Contains(output, []byte("--tar=")), nil
}

// MkfsConvertTar converts a tar stream r into a full EROFS image at layerPath
// with mkfs.erofs, passing mkfsExtraOpts through.
func MkfsConvertTar(ctx context.Context, r io.Reader, layerPath, uuid string, mkfsExtraOpts []string) error {
	args := append([]string{"--tar=f", "--aufs", "--quiet", "-Enoinline_data"}, mkfsExtraOpts...)
	if uuid != "" {
		args = append(args, []string{"-U", uuid}...)
	}
	args = append(args, layerPath)
	cmd := exec.CommandContext(ctx, "mkfs.erofs", args...)
	cmd.Stdin = r
	cmd.Env = mkfsEnv(layerPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("erofs apply failed: %s: %w", out, err)
	}
	log.G(ctx).Debugf("running %s %s %v", cmd.Path, cmd.Args, string(out))
	return nil
}

// MkfsGenerateTarIndex generates a tar index for the tar stream r with
// mkfs.erofs --tar=i and appends the original tar content, producing a single
// self-contained layer at layerPath: [Tar index][Original tar content].
func MkfsGenerateTarIndex(ctx context.Context, r io.Reader, layerPath, uuid string, mkfsExtraOpts []string) error {
	// Create a temporary file for storing the tar content
	tarFile, err := os.CreateTemp("", "erofs-tar-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary tar file: %w", err)
	}
	defer os.Remove(tarFile.Name())
	defer tarFile.Close()

	// Use TeeReader to process the input once while saving it to disk
	teeReader := io.TeeReader(r, tarFile)

	// Generate tar index directly to layerPath using --tar=i option
	args := append([]string{"--tar=i", "--aufs", "--quiet"}, mkfsExtraOpts...)
	if uuid != "" {
		args = append(args, []string{"-U", uuid}...)
	}
	args = append(args, layerPath)
	cmd := exec.CommandContext(ctx, "mkfs.erofs", args...)
	cmd.Stdin = teeReader
	cmd.Env = mkfsEnv(layerPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar index generation failed with command 'mkfs.erofs %s': %s: %w",
			strings.Join(args, " "), out, err)
	}
	log.G(ctx).Debugf("running %s %v %s", cmd.Path, cmd.Args, string(out))

	// Open layerPath for appending
	f, err := os.OpenFile(layerPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open layer file for appending: %w", err)
	}
	defer f.Close()

	// Rewind the temporary file
	if _, err := tarFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek to the beginning of tar file: %w", err)
	}

	// Append tar content
	if _, err := io.Copy(f, tarFile); err != nil {
		return fmt.Errorf("failed to append tar to layer: %w", err)
	}

	log.G(ctx).Infof("Successfully generated EROFS layer with tar index and tar content: %s", layerPath)

	return nil
}

// AddDefaultMkfsOpts adds default options for mkfs.erofs
func AddDefaultMkfsOpts(mkfsExtraOpts []string) []string {
	// Check if -b argument is already present
	for _, opt := range mkfsExtraOpts {
		if strings.HasPrefix(opt, "-b") {
			return mkfsExtraOpts
		}
	}

	// Default to a 4K block size so images mount on any page size.
	return append([]string{"-b4096"}, mkfsExtraOpts...)
}
