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

package runc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	pkgrunc "github.com/containerd/go-runc"
	"github.com/containerd/log"
	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/containerd/containerd/v2/pkg/atomicfile"
)

// ShouldKillAllOnExit reads the bundle's OCI spec and returns true if
// there is an error reading the spec or if the container has a private PID namespace
func ShouldKillAllOnExit(ctx context.Context, bundlePath string) bool {
	spec, err := readSpec(bundlePath)
	if err != nil {
		log.G(ctx).WithError(err).Error("shouldKillAllOnExit: failed to read config.json")
		return true
	}

	if spec.Linux != nil {
		for _, ns := range spec.Linux.Namespaces {
			if ns.Type == specs.PIDNamespace && ns.Path == "" {
				return false
			}
		}
	}
	return true
}

func readSpec(p string) (*specs.Spec, error) {
	const configFileName = "config.json"
	f, err := os.Open(filepath.Join(p, configFileName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s specs.Spec
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

const exitStatusFileName = "exit.json"

// WriteExitStatus stores exit status for exited container process.
func WriteExitStatus(bundlePath string, e pkgrunc.Exit) error {
	exitValue, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("failed to marshal runc.Exit value: %w", err)
	}

	f, err := atomicfile.New(filepath.Join(bundlePath, exitStatusFileName), 0600)
	if err != nil {
		return fmt.Errorf("failed to create exit status file: %w", err)
	}
	if _, err := f.Write(exitValue); err != nil {
		_ = f.Cancel()
		return fmt.Errorf("failed to write exit status: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to commit exit status: %w", err)
	}
	return nil
}

// ReadExitStatus reads exit status of exited container process.
func ReadExitStatus(bundlePath string) (pkgrunc.Exit, error) {
	exitFile := filepath.Join(bundlePath, exitStatusFileName)
	f, err := os.Open(exitFile)
	if err != nil {
		return pkgrunc.Exit{}, fmt.Errorf("failed to open %s: %w", exitFile, err)
	}
	defer f.Close()

	var e pkgrunc.Exit
	if err := json.NewDecoder(f).Decode(&e); err != nil {
		return pkgrunc.Exit{}, fmt.Errorf("failed to unmarshal runc.Exit: %w", err)
	}
	if e.Pid <= 0 || e.Timestamp.IsZero() || e.Status < 0 {
		return pkgrunc.Exit{}, fmt.Errorf("invalid runc.Exit value: %v", e)
	}
	return e, nil
}
