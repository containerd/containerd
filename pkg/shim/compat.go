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

package shim

// This file contains the compatibility layer between the new shim bootstrap
// protocol (see https://github.com/containerd/containerd/pull/12786) and the
// old shim APIs (prior containerd 2.3), which mainly relies on CLI, env vars, stdin, and spec.json annotations.
// Once settled, this file should be removed.

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
)

var errDeprecatedBootstrapAPI = errors.New("shim was started through the deprecated API but was built against the new API; if you upgraded containerd, you may need to restart the daemon")

// parseBootstrapParams parses input from a caller using the bootstrap API.
// Legacy runtime options can unmarshal as BootstrapParams, so verify the
// identity against the command-line values.
func parseBootstrapParams(input []byte, cliID, cliNamespace string) (*bootapi.BootstrapParams, error) {
	params := &bootapi.BootstrapParams{}
	if len(input) == 0 || proto.Unmarshal(input, params) != nil {
		return nil, errDeprecatedBootstrapAPI
	}
	if params.InstanceID != cliID || params.Namespace != cliNamespace {
		return nil, errDeprecatedBootstrapAPI
	}
	return params, nil
}

func readBootstrapParamsFromDeprecatedFields(input []byte, params *bootapi.BootstrapParams, parsedID string, parsedNamespace string, parsedBinary string, parsedDebug bool) error {
	params.InstanceID = parsedID
	params.Namespace = parsedNamespace
	params.ContainerdTtrpcAddress = os.Getenv(ttrpcAddressEnv)
	params.ContainerdGrpcAddress = os.Getenv(grpcAddressEnv)
	params.ContainerdBinary = parsedBinary

	if parsedDebug {
		params.LogLevel = bootapi.LogLevel_LOG_LEVEL_DEBUG
	}

	// Task options

	if opts, err := ReadRuntimeOptions[*options.Options](bytes.NewBuffer(input)); err == nil {
		if err := params.AddExtension(opts); err != nil {
			return fmt.Errorf("unable to add runc options: %w", err)
		}
	}

	return nil
}
