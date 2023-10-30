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

package v2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/errdefs"
	client "github.com/containerd/containerd/v2/runtime/v2/shim"
	"github.com/stretchr/testify/require"
)

func TestParseStartResponse(t *testing.T) {
	for _, tc := range []struct {
		Name     string
		Response string
		Expected client.BootstrapParams
		Err      error
	}{
		{
			Name:     "v2 shim",
			Response: "/somedirectory/somesocket",
			Expected: client.BootstrapParams{
				Version:  2,
				Address:  "/somedirectory/somesocket",
				Protocol: "ttrpc",
			},
		},
		{
			Name:     "v2 shim using grpc",
			Response: `{"version":2,"address":"/somedirectory/somesocket","protocol":"grpc"}`,
			Expected: client.BootstrapParams{
				Version:  2,
				Address:  "/somedirectory/somesocket",
				Protocol: "grpc",
			},
		},
		{
			Name:     "v2 shim using ttrpc",
			Response: `{"version":2,"address":"/somedirectory/somesocket","protocol":"ttrpc"}`,
			Expected: client.BootstrapParams{
				Version:  2,
				Address:  "/somedirectory/somesocket",
				Protocol: "ttrpc",
			},
		},
		{
			Name:     "invalid shim v2 response",
			Response: `{"address":"/somedirectory/somesocket","protocol":"ttrpc"}`,
			Expected: client.BootstrapParams{
				Version:  2,
				Address:  `{"address":"/somedirectory/somesocket","protocol":"ttrpc"}`,
				Protocol: "ttrpc",
			},
		},
		{
			Name:     "later unsupported shim",
			Response: `{"Version": 4,"Address":"/somedirectory/somesocket","Protocol":"ttrpc"}`,
			Expected: client.BootstrapParams{},
			Err:      errdefs.ErrNotImplemented,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			params, err := parseStartResponse([]byte(tc.Response))
			if err != nil {
				if !errors.Is(err, tc.Err) {
					t.Errorf("unexpected error: %v", err)
				}
				return
			} else if tc.Err != nil {
				t.Fatal("expected error")
			}
			if params.Version != tc.Expected.Version {
				t.Errorf("unexpected version %d, expected %d", params.Version, tc.Expected.Version)
			}
			if params.Protocol != tc.Expected.Protocol {
				t.Errorf("unexpected protocol %q, expected %q", params.Protocol, tc.Expected.Protocol)
			}
			if params.Address != tc.Expected.Address {
				t.Errorf("unexpected address %q, expected %q", params.Address, tc.Expected.Address)
			}
		})
	}
}

func TestRestoreBootstrapParams(t *testing.T) {
	bundlePath := t.TempDir()

	err := os.WriteFile(filepath.Join(bundlePath, "address"), []byte("unix://123"), 0o666)
	require.NoError(t, err)

	restored, err := restoreBootstrapParams(bundlePath)
	require.NoError(t, err)

	expected := client.BootstrapParams{
		Version:  2,
		Address:  "unix://123",
		Protocol: "ttrpc",
	}

	require.EqualValues(t, expected, restored)

	loaded, err := readBootstrapParams(filepath.Join(bundlePath, "bootstrap.json"))

	require.NoError(t, err)
	require.EqualValues(t, expected, loaded)
}
