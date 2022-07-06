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

package opts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/windows"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	osinterface "github.com/containerd/containerd/pkg/os"
)

// WithWindowsNetworkNamespace sets windows network namespace for container.
// TODO(windows): Move this into container/containerd.
func WithWindowsNetworkNamespace(path string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		if s.Windows.Network == nil {
			s.Windows.Network = &runtimespec.WindowsNetwork{}
		}
		s.Windows.Network.NetworkNamespace = path
		return nil
	}
}

// namedPipePath returns true if the given path is to a named pipe.
func namedPipePath(p string) bool {
	return strings.HasPrefix(p, `\\.\pipe\`)
}

// cleanMount returns a cleaned version of the mount path. The input is returned
// as-is if it is a named pipe path.
func cleanMount(p string) string {
	if namedPipePath(p) {
		return p
	}
	return filepath.Clean(p)
}

func parseMount(osi osinterface.OS, mount *runtime.Mount) (*runtimespec.Mount, error) {
	var (
		dst = mount.GetContainerPath()
		src = mount.GetHostPath()
	)
	// In the case of a named pipe mount on Windows, don't stat the file
	// or do other operations that open it, as that could interfere with
	// the listening process. filepath.Clean also breaks named pipe
	// paths, so don't use it.
	if !namedPipePath(src) {
		if _, err := osi.Stat(src); err != nil {
			// Create the host path if it doesn't exist. This will align
			// the behavior with the Linux implementation, but it doesn't
			// align with Docker's behavior on Windows.
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to stat %q: %w", src, err)
			}
			if err := osi.MkdirAll(src, 0755); err != nil {
				return nil, fmt.Errorf("failed to mkdir %q: %w", src, err)
			}
		}
		var err error
		src, err = osi.ResolveSymbolicLink(src)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve symlink %q: %w", src, err)
		}
		// hcsshim requires clean path, especially '/' -> '\'. Additionally,
		// for the destination, absolute paths should have the C: prefix.
		src = filepath.Clean(src)

		// filepath.Clean adds a '.' at the end if the path is a
		// drive (like Z:, E: etc.). Keeping this '.' in the path
		// causes incorrect parameter error when starting the
		// container on windows.  Remove it here.
		if !(len(dst) == 2 && dst[1] == ':') {
			dst = filepath.Clean(dst)
			if dst[0] == '\\' {
				dst = "C:" + dst
			}
		} else if dst[0] == 'c' || dst[0] == 'C' {
			return nil, fmt.Errorf("destination path can not be C drive")
		}
	}

	var options []string
	// NOTE(random-liu): we don't change all mounts to `ro` when root filesystem
	// is readonly. This is different from docker's behavior, but make more sense.
	if mount.GetReadonly() {
		options = append(options, "ro")
	} else {
		options = append(options, "rw")
	}
	return &runtimespec.Mount{Source: src, Destination: dst, Options: options}, nil
}

// WithWindowsMounts sorts and adds runtime and CRI mounts to the spec for
// windows container.
func WithWindowsMounts(osi osinterface.OS, config *runtime.ContainerConfig, extra []*runtime.Mount) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		// mergeMounts merge CRI mounts with extra mounts. If a mount destination
		// is mounted by both a CRI mount and an extra mount, the CRI mount will
		// be kept.
		var (
			criMounts = config.GetMounts()
			mounts    = append([]*runtime.Mount{}, criMounts...)
		)
		// Copy all mounts from extra mounts, except for mounts overridden by CRI.
		for _, e := range extra {
			found := false
			for _, c := range criMounts {
				if cleanMount(e.ContainerPath) == cleanMount(c.ContainerPath) {
					found = true
					break
				}
			}
			if !found {
				mounts = append(mounts, e)
			}
		}

		// Sort mounts in number of parts. This ensures that high level mounts don't
		// shadow other mounts.
		sort.Sort(orderedMounts(mounts))

		// Copy all mounts from default mounts, except for
		// mounts overridden by supplied mount;
		mountSet := make(map[string]struct{})
		for _, m := range mounts {
			mountSet[cleanMount(m.ContainerPath)] = struct{}{}
		}

		defaultMounts := s.Mounts
		s.Mounts = nil

		for _, m := range defaultMounts {
			dst := cleanMount(m.Destination)
			if _, ok := mountSet[dst]; ok {
				// filter out mount overridden by a supplied mount
				continue
			}
			s.Mounts = append(s.Mounts, m)
		}

		for _, mount := range mounts {
			parsedMount, err := parseMount(osi, mount)
			if err != nil {
				return err
			}
			s.Mounts = append(s.Mounts, *parsedMount)
		}
		return nil
	}
}

// WithWindowsResources sets the provided resource restrictions for windows.
func WithWindowsResources(resources *runtime.WindowsContainerResources) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if resources == nil {
			return nil
		}
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		if s.Windows.Resources == nil {
			s.Windows.Resources = &runtimespec.WindowsResources{}
		}
		if s.Windows.Resources.Memory == nil {
			s.Windows.Resources.Memory = &runtimespec.WindowsMemoryResources{}
		}

		var (
			count  = uint64(resources.GetCpuCount())
			shares = uint16(resources.GetCpuShares())
			max    = uint16(resources.GetCpuMaximum())
			limit  = uint64(resources.GetMemoryLimitInBytes())
		)
		if s.Windows.Resources.CPU == nil && (count != 0 || shares != 0 || max != 0) {
			s.Windows.Resources.CPU = &runtimespec.WindowsCPUResources{}
		}
		if count != 0 {
			s.Windows.Resources.CPU.Count = &count
		}
		if shares != 0 {
			s.Windows.Resources.CPU.Shares = &shares
		}
		if max != 0 {
			s.Windows.Resources.CPU.Maximum = &max
		}
		if limit != 0 {
			s.Windows.Resources.Memory.Limit = &limit
		}
		return nil
	}
}

// WithWindowsDefaultSandboxShares sets the default sandbox CPU shares
func WithWindowsDefaultSandboxShares(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
	if s.Windows == nil {
		s.Windows = &runtimespec.Windows{}
	}
	if s.Windows.Resources == nil {
		s.Windows.Resources = &runtimespec.WindowsResources{}
	}
	if s.Windows.Resources.CPU == nil {
		s.Windows.Resources.CPU = &runtimespec.WindowsCPUResources{}
	}
	i := uint16(DefaultSandboxCPUshares)
	s.Windows.Resources.CPU.Shares = &i
	return nil
}

// WithWindowsCredentialSpec assigns `credentialSpec` to the
// `runtime.Spec.Windows.CredentialSpec` field.
func WithWindowsCredentialSpec(credentialSpec string) oci.SpecOpts {
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) error {
		if s.Windows == nil {
			s.Windows = &runtimespec.Windows{}
		}
		s.Windows.CredentialSpec = credentialSpec
		return nil
	}
}

func escapeAndCombineArgsWindows(args []string) string {
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = windows.EscapeArg(a)
	}
	return strings.Join(escaped, " ")
}

// WithProcessCommandLineOrArgsForWindows sets the process command line or process args on the spec based on the image
// and runtime config
// If image.ArgsEscaped field is set, this function sets the process command line and if not, it sets the
// process args field
func WithProcessCommandLineOrArgsForWindows(config *runtime.ContainerConfig, image *imagespec.ImageConfig) oci.SpecOpts {
	if image.ArgsEscaped {
		return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
			// firstArgFromImg is a flag that is returned to indicate that the first arg in the slice comes from either the
			// image Entrypoint or Cmd. If the first arg instead comes from the container config (e.g. overriding the image values),
			// it should be false. This is done to support the non-OCI ArgsEscaped field that Docker used to determine how the image
			// entrypoint and cmd should be interpreted.
			//
			args, firstArgFromImg, err := getArgs(image.Entrypoint, image.Cmd, config.GetCommand(), config.GetArgs())
			if err != nil {
				return err
			}

			var cmdLine string
			if image.ArgsEscaped && firstArgFromImg {
				cmdLine = args[0]
				if len(args) > 1 {
					cmdLine += " " + escapeAndCombineArgsWindows(args[1:])
				}
			} else {
				cmdLine = escapeAndCombineArgsWindows(args)
			}

			return oci.WithProcessCommandLine(cmdLine)(ctx, client, c, s)
		}
	}
	// if ArgsEscaped is not set
	return func(ctx context.Context, client oci.Client, c *containers.Container, s *runtimespec.Spec) (err error) {
		args, _, err := getArgs(image.Entrypoint, image.Cmd, config.GetCommand(), config.GetArgs())
		if err != nil {
			return err
		}
		return oci.WithProcessArgs(args...)(ctx, client, c, s)
	}
}

// getArgs is used to evaluate the overall args for the container by taking into account the image command and entrypoints
// along with the container command and entrypoints specified through the podspec if any
func getArgs(imgEntrypoint []string, imgCmd []string, ctrEntrypoint []string, ctrCmd []string) ([]string, bool, error) {
	//nolint:dupword
	// firstArgFromImg is a flag that is returned to indicate that the first arg in the slice comes from either the image
	// Entrypoint or Cmd. If the first arg instead comes from the container config (e.g. overriding the image values),
	// it should be false.
	// Essentially this means firstArgFromImg should be true iff:
	// Ctr entrypoint	ctr cmd		image entrypoint	image cmd	firstArgFromImg
	// --------------------------------------------------------------------------------
	//	nil				 nil			exists			 nil		  true
	//  nil				 nil		    nil				 exists		  true

	// This is needed to support the non-OCI ArgsEscaped field used by Docker. ArgsEscaped is used for
	// Windows images to indicate that the command has already been escaped and should be
	// used directly as the command line.
	var firstArgFromImg bool
	entrypoint, cmd := ctrEntrypoint, ctrCmd
	// The following logic is migrated from https://github.com/moby/moby/blob/master/daemon/commit.go
	// TODO(random-liu): Clearly define the commands overwrite behavior.
	if len(entrypoint) == 0 {
		// Copy array to avoid data race.
		if len(cmd) == 0 {
			cmd = append([]string{}, imgCmd...)
			if len(imgCmd) > 0 {
				firstArgFromImg = true
			}
		}
		if entrypoint == nil {
			entrypoint = append([]string{}, imgEntrypoint...)
			if len(imgEntrypoint) > 0 || len(ctrCmd) == 0 {
				firstArgFromImg = true
			}
		}
	}
	if len(entrypoint) == 0 && len(cmd) == 0 {
		return nil, false, errors.New("no command specified")
	}
	return append(entrypoint, cmd...), firstArgFromImg, nil
}
