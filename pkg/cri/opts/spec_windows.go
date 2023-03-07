//go:build windows

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
	"strings"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/windows"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

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
