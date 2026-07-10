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
	"encoding/json"
	"fmt"
	"os"

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	"github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
)

// parseBootstrapParams decodes the shim start payload and reports whether it
// uses the new bootstrap protocol.
//
// The runtime-options Any used by pre-2.3 containerd shares protobuf field
// numbers with BootstrapParams, so both payloads unmarshal successfully.
// Compare the decoded instance ID and namespace with the command-line values
// to distinguish the two.
func parseBootstrapParams(input []byte, parsedID, parsedNamespace string) (*bootapi.BootstrapParams, bool) {
	params := &bootapi.BootstrapParams{}
	if len(input) == 0 || proto.Unmarshal(input, params) != nil {
		return &bootapi.BootstrapParams{}, false
	}

	// TODO: Remove CLI identity matching together with support for the
	// deprecated startup fields.
	if params.InstanceID != parsedID || params.Namespace != parsedNamespace {
		// Do not let fields decoded from a legacy or malformed payload affect
		// bootstrap behavior.
		return &bootapi.BootstrapParams{}, false
	}
	return params, true
}

func marshalBootstrapResponse(result *bootapi.BootstrapResult, usesBootstrapProtocol bool) ([]byte, error) {
	if usesBootstrapProtocol {
		return proto.Marshal(result)
	}

	// containerd 2.0-2.2 expects a JSON BootstrapParams response.
	// This compatibility path does not support containerd 1.x.
	// The JSON envelope is legacy, but Version selects the task service API,
	// so preserve the shim result.
	// Marshal the legacy type to preserve its field names and omit unsupported
	// capabilities and metadata.
	return json.Marshal(&BootstrapParams{ //nolint:staticcheck // Required for compatibility with containerd 2.0-2.2.
		Version:  int(result.Version),
		Address:  result.Address,
		Protocol: result.Protocol,
	})
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
