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

package securityprofile

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/pkg/atomicfile"
)

type Delivery struct {
	labelPrefix string
	targetDir   string
}

type Errors struct {
	NotFound    error
	InvalidData error
}

func New(labelPrefix, targetDir string) (*Delivery, error) {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("resolve target directory: %w", err)
	}
	return &Delivery{
		labelPrefix: labelPrefix,
		targetDir:   absTarget,
	}, nil
}

func (d *Delivery) Materialize(ref string, labels map[string]string, errs Errors) (string, bool, error) {
	ref = strings.TrimPrefix(ref, d.targetDir+string(os.PathSeparator))
	key := d.labelPrefix + ref
	value := strings.TrimSpace(labels[key])
	if value == "" {
		if errs.NotFound != nil {
			return "", false, fmt.Errorf("%w: %s", errs.NotFound, key)
		}
		return "", false, fmt.Errorf("profile not found: %s", key)
	}
	data, err := decodeBase64(value)
	if err != nil {
		if errs.InvalidData != nil {
			return "", false, fmt.Errorf("%w: %v", errs.InvalidData, err)
		}
		return "", false, fmt.Errorf("invalid profile data: %w", err)
	}
	dest := filepath.Join(d.targetDir, ref)
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", false, fmt.Errorf("resolve destination path: %w", err)
	}
	if !strings.HasPrefix(absDest, d.targetDir+string(os.PathSeparator)) && absDest != d.targetDir {
		return "", false, fmt.Errorf("invalid ref: path traversal detected")
	}
	if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
		return "", false, fmt.Errorf("create profile directory: %w", err)
	}
	// Resolve symlinks in the parent directory to prevent symlink traversal
	// (e.g. a symlink inside targetDir pointing outside it).
	realDir, err := filepath.EvalSymlinks(filepath.Dir(absDest))
	if err != nil {
		return "", false, fmt.Errorf("resolve real path of profile directory: %w", err)
	}
	if !strings.HasPrefix(realDir, d.targetDir+string(os.PathSeparator)) && realDir != d.targetDir {
		return "", false, fmt.Errorf("invalid ref: symlink traversal detected")
	}
	existing, err := os.ReadFile(absDest)
	if err == nil {
		if bytes.Equal(existing, data) {
			return absDest, false, nil
		}
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("read existing profile: %w", err)
	}
	// Atomic write: temp file + fsync + rename to avoid partial reads
	f, err := atomicfile.New(absDest, defaultFileMode)
	if err != nil {
		return "", false, fmt.Errorf("create profile file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Cancel()
		return "", false, fmt.Errorf("write profile data: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", false, fmt.Errorf("finalize profile: %w", err)
	}
	return absDest, true, nil
}

const defaultFileMode = 0o640

func decodeBase64(value string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.URLEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	if data, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("decode base64: input is not valid base64")
}
