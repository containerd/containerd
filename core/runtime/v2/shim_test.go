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

	bootapi "github.com/containerd/containerd/api/runtime/bootstrap/v1"
	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/pkg/protobuf/proto"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	googleproto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// requireProtoEqual compares two protobuf messages by value. Comparing them
// with reflect-based deep equality is not sound, as the generated types carry
// internal state that differs depending on how the message was constructed.
func requireProtoEqual(t *testing.T, expected, actual googleproto.Message) {
	t.Helper()
	if !googleproto.Equal(expected, actual) {
		t.Fatalf("unexpected message:\n  expected: %s\n  actual:   %s",
			prototext.Format(expected), prototext.Format(actual))
	}
}

func TestParseStartResponse(t *testing.T) {
	protobufResponse, err := proto.Marshal(&bootapi.BootstrapResult{
		Version:  3,
		Address:  "unix:///run/containerd/shim.sock",
		Protocol: "ttrpc",
		Metadata: map[string]string{"note": "line\n"},
	})
	require.NoError(t, err)
	require.Equal(t, byte('\n'), protobufResponse[len(protobufResponse)-1])

	testCases := []struct {
		Name     string
		Response []byte
		Expected *bootapi.BootstrapResult
		Err      error
	}{
		{
			Name:     "v2 shim with trailing newline",
			Response: []byte("/somedirectory/somesocket\n"),
			Expected: &bootapi.BootstrapResult{
				Version:  2,
				Address:  "/somedirectory/somesocket",
				Protocol: "ttrpc",
			},
		},
		{
			Name:     "v3 shim protobuf",
			Response: protobufResponse,
			Expected: &bootapi.BootstrapResult{
				Version:  3,
				Address:  "unix:///run/containerd/shim.sock",
				Protocol: "ttrpc",
				Metadata: map[string]string{"note": "line\n"},
			},
		},
		{
			Name:     "v2 shim using grpc",
			Response: []byte(`{"version":2,"address":"/somedirectory/somesocket","protocol":"grpc"}`),
			Expected: &bootapi.BootstrapResult{
				Version:  2,
				Address:  "/somedirectory/somesocket",
				Protocol: "grpc",
			},
		},
		{
			Name:     "v2 shim using ttrpc",
			Response: []byte(`{"version":2,"address":"/somedirectory/somesocket","protocol":"ttrpc"}`),
			Expected: &bootapi.BootstrapResult{
				Version:  2,
				Address:  "/somedirectory/somesocket",
				Protocol: "ttrpc",
			},
		},
		{
			// A JSON response carries the whole message, not just the three
			// fields needed to connect. This is the form a bundle's
			// bootstrap.json is stored in and read back through.
			//
			// The capability value is one this version of containerd does not
			// define; it must still round-trip so that a future capability is
			// not silently discarded on reload.
			Name: "json with an unrecognized capability and an extension",
			Response: []byte(`{"version":3,"address":"/somedirectory/somesocket","protocol":"ttrpc",` +
				`"capabilities":[7],"metadata":{"a":"b"},` +
				`"extensions":[{"value":{"type_url":"type.googleapis.com/containerd.types.MountCapabilities","value":"CgVlcm9mcw=="}}]}`),
			Expected: &bootapi.BootstrapResult{
				Version:      3,
				Address:      "/somedirectory/somesocket",
				Protocol:     "ttrpc",
				Capabilities: []bootapi.Capability{7},
				Metadata:     map[string]string{"a": "b"},
				Extensions: []*bootapi.Extension{{
					Value: &anypb.Any{
						TypeUrl: "type.googleapis.com/containerd.types.MountCapabilities",
						Value:   []byte("\n\x05erofs"),
					},
				}},
			},
		},
		{
			Name:     "invalid shim v2 response",
			Response: []byte(`{"address":"/somedirectory/somesocket","protocol":"ttrpc"}`),
			Expected: &bootapi.BootstrapResult{
				Version:  2,
				Address:  `{"address":"/somedirectory/somesocket","protocol":"ttrpc"}`,
				Protocol: "ttrpc",
			},
		},
		{
			Name:     "later unsupported shim",
			Response: []byte(`{"Version": 4,"Address":"/somedirectory/somesocket","Protocol":"ttrpc"}`),
			Expected: &bootapi.BootstrapResult{},
			Err:      errdefs.ErrNotImplemented,
		},
	}

	for i := range testCases {
		tc := &testCases[i]
		t.Run(tc.Name, func(t *testing.T) {
			params, err := parseStartResponse(tc.Response)
			if err != nil {
				if !errors.Is(err, tc.Err) {
					t.Errorf("unexpected error: %v", err)
				}
				return
			} else if tc.Err != nil {
				t.Fatal("expected error")
			}
			requireProtoEqual(t, tc.Expected, params)
		})
	}
}

func TestRestoreBootstrapParams(t *testing.T) {
	bundlePath := t.TempDir()

	err := os.WriteFile(filepath.Join(bundlePath, "address"), []byte("unix://123"), 0o666)
	require.NoError(t, err)

	restored, err := restoreBootstrapParams(bundlePath)
	require.NoError(t, err)

	expected := &bootapi.BootstrapResult{
		Version:  2,
		Address:  "unix://123",
		Protocol: "ttrpc",
	}

	requireProtoEqual(t, expected, restored)

	loaded, err := readBootstrapParams(filepath.Join(bundlePath, "bootstrap.json"))

	require.NoError(t, err)
	requireProtoEqual(t, expected, loaded)
}

// TestBootstrapParamsRoundTrip ensures every field of BootstrapResult survives
// being written to and read back from a bundle.
//
// Extensions must survive in particular, because a container joining an
// existing sandbox shim recovers that shim's capabilities from this file
// rather than from a fresh shim start.
func TestBootstrapParamsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")

	written := &bootapi.BootstrapResult{
		Version:  3,
		Address:  "unix:///run/containerd/shim.sock",
		Protocol: "ttrpc",
		Metadata: map[string]string{"note": "line\n"},
	}
	require.NoError(t, written.AddExtension(&apitypes.MountCapabilities{
		Types:      []string{"erofs", "loop"},
		Transforms: []string{"format", "mkfs"},
	}))

	require.NoError(t, writeBootstrapParams(path, written))

	loaded, err := readBootstrapParams(path)
	require.NoError(t, err)
	requireProtoEqual(t, written, loaded)

	var mc apitypes.MountCapabilities
	found, err := loaded.FindExtension(&mc)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []string{"erofs", "loop"}, mc.Types)
	require.Equal(t, []string{"format", "mkfs"}, mc.Transforms)
}

// TestBootstrapParamsUnknownExtension ensures an extension whose type is not
// linked into containerd is preserved rather than rejected or dropped, as the
// protocol requires. A shim must be able to advertise a capability the daemon
// knows nothing about without preventing the daemon from recording how to
// reach it.
//
// encoding/json is what makes this work: it sees an Any as an opaque type URL
// and byte slice, and so never has to resolve the type.
func TestBootstrapParamsUnknownExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")

	unknown := &anypb.Any{
		TypeUrl: "type.googleapis.com/example.com.SomeFutureCapability",
		Value:   []byte{0x0a, 0x03, 'a', 'b', 'c'},
	}

	written := &bootapi.BootstrapResult{
		Version:  3,
		Address:  "unix:///run/containerd/shim.sock",
		Protocol: "ttrpc",
	}
	require.NoError(t, written.AddExtension(unknown))

	require.NoError(t, writeBootstrapParams(path, written))

	loaded, err := readBootstrapParams(path)
	require.NoError(t, err)
	requireProtoEqual(t, written, loaded)

	// Looking for a known extension simply does not find it.
	var mc apitypes.MountCapabilities
	found, err := loaded.FindExtension(&mc)
	require.NoError(t, err)
	require.False(t, found)
}

// TestReadBootstrapParamsLegacy ensures bundles written by earlier versions,
// which used encoding/json, are still readable.
func TestReadBootstrapParamsLegacy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
	}{
		{
			name:     "encoding/json",
			contents: `{"version":3,"address":"unix:///run/shim.sock","protocol":"ttrpc"}`,
		},
		{
			name:     "encoding/json with metadata",
			contents: `{"version":3,"address":"unix:///run/shim.sock","protocol":"ttrpc","metadata":{"a":"b"}}`,
		},
		{
			name:     "bare address",
			contents: "unix:///run/shim.sock",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bootstrap.json")
			require.NoError(t, os.WriteFile(path, []byte(tc.contents), 0o600))

			loaded, err := readBootstrapParams(path)
			require.NoError(t, err)
			require.Equal(t, "unix:///run/shim.sock", loaded.Address)
			require.Equal(t, "ttrpc", loaded.Protocol)
		})
	}
}
