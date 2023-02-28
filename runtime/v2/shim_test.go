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
	"context"
	"errors"
	"testing"

	"github.com/containerd/containerd/errdefs"
	client "github.com/containerd/containerd/runtime/v2/shim"
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
				Version:  0,
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
				Version:  0,
				Address:  `{"address":"/somedirectory/somesocket","protocol":"ttrpc"}`,
				Protocol: "ttrpc",
			},
		},
		{
			Name:     "later unsupported shim",
			Response: `{"Version": 3,"Address":"/somedirectory/somesocket","Protocol":"ttrpc"}`,
			Expected: client.BootstrapParams{},
			Err:      errdefs.ErrNotImplemented,
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			params, err := parseStartResponse(context.Background(), []byte(tc.Response))
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
