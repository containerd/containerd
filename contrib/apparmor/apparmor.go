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

package apparmor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// WithProfile sets the provided apparmor profile to the spec
func WithProfile(profile string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		s.Process.ApparmorProfile = profile
		return nil
	}
}

// WithDefaultProfile will generate a default apparmor profile under the provided name
// for the container.  It is only generated if a profile under that name does not exist.
func WithDefaultProfile(name string) oci.SpecOpts {
	return func(ctx context.Context, _ oci.Client, _ *containers.Container, s *specs.Spec) error {
		if err := loadDefaultProfile(ctx, name); err != nil {
			return err
		}
		s.Process.ApparmorProfile = name
		return nil
	}
}

// LoadDefaultProfile ensures the default profile to be loaded with the given name.
// Returns nil error if the profile is already loaded.
func LoadDefaultProfile(name string) error {
	return loadDefaultProfile(context.Background(), name)
}

func loadDefaultProfile(ctx context.Context, name string) error {
	yes, err := isLoaded(name)
	if err != nil {
		return err
	}
	if yes {
		return nil
	}
	p, err := loadData(name)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := generate(p, &buf); err != nil {
		return err
	}
	if err := loadProfile(ctx, &buf); err != nil {
		return fmt.Errorf("load AppArmor profile: %w", err)
	}
	return nil
}

// DumpDefaultProfile dumps the default profile with the given name.
func DumpDefaultProfile(name string) (string, error) {
	p, err := loadData(name)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := generate(p, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func isLoaded(name string) (bool, error) {
	f, err := os.Open("/sys/kernel/security/apparmor/profiles")
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Entries are of the form "<profile> (<mode>)", e.g. "foo (enforce)".
		// Profile names may contain spaces (quoted names are supported in AppArmor),
		// so split on " (" rather than the first space.
		if prefix, _, ok := strings.Cut(scanner.Text(), " ("); ok && prefix == name {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// loadProfile runs "apparmor_parser -Kr", providing the AppArmor profile on
// stdin to replace the profile. The "-K" is necessary to make sure that
// apparmor_parser doesn't try to write to a read-only filesystem.
func loadProfile(ctx context.Context, profile io.Reader) error {
	c := exec.CommandContext(ctx, "apparmor_parser", "-Kr")
	c.Stdin = profile

	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("parser error(%q): %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}
