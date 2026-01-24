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

	bootapi "github.com/containerd/containerd/api/runtime/boot/v1"
	"github.com/containerd/containerd/api/types/runc/options"
)

func readBootstrapParamsFromDeprecatedFields(input []byte, params *BootstrapParams) error {
	params.ID = id
	params.Namespace = namespaceFlag
	params.ContainerdTtrpcAddress = os.Getenv(ttrpcAddressEnv)
	params.ContainerdGrpcAddress = os.Getenv(grpcAddressEnv)
	params.SocketPath = socketFlag
	params.SocketPathDebug = debugSocketFlag
	params.ContainerdBinary = containerdBinaryFlag
	params.EnableDebug = debugFlag

	// Runc v2 specific extensions

	if opts, err := ReadRuntimeOptions[*options.Options](bytes.NewBuffer(input)); err == nil {
		if err := params.AddExtension(opts); err != nil {
			return fmt.Errorf("unable to add runc options: %w", err)
		}
	}

	var runcExt bootapi.RuncV2Extensions

	if spec, err := readSpec(); err == nil {
		runcExt.SpecAnnotations = spec.Annotations
	}

	if err := params.AddExtension(&runcExt); err != nil {
		return fmt.Errorf("unable to add runc v2 extensions: %w", err)
	}

	return nil
}

// spec is a shallow version of [oci.Spec] used by Runc V2 shim,
// containing only the fields we need for the hook. We use a shallow struct to reduce
// the overhead of unmarshaling.
type spec struct {
	// Annotations contains arbitrary metadata for the container.
	Annotations map[string]string `json:"annotations,omitempty"`
}

func readSpec() (*spec, error) {
	const configFileName = "config.json"
	f, err := os.Open(configFileName)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s spec
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}
