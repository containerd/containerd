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

package cap

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, input, encoded string
		rootID               uint32
		namespaced           bool
	}{
		{"revision1", "010000010010000002000000", "010000030010000002000000000000000000000000000000", 0, false},
		{"revision2", "0100000200100000020000001000000020000000", "010000030010000002000000100000002000000000000000", 0, false},
		{"revision3", "01000003001000000200000010000000200000002a000000", "01000003001000000200000010000000200000002a000000", 42, true},
		{"no-effective-flag", "0000000200100000000000000000000000000000", "000000030010000000000000000000000000000000000000", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := hex.DecodeString(tc.input)
			require.NoError(t, err)
			expected, err := hex.DecodeString(tc.encoded)
			require.NoError(t, err)
			caps, err := ParseFileCapabilities(input)
			require.NoError(t, err)
			require.Equal(t, tc.rootID, caps.RootID)
			require.Equal(t, tc.namespaced, caps.Namespaced())
			require.Equal(t, expected, caps.Encode())
			require.Equal(t, tc.input, hex.EncodeToString(input), "decoding and encoding must not mutate input")
			caps.RootID = 100000
			updated := caps.Encode()
			require.Equal(t, expected[:size2], updated[:size2], "changing root must preserve masks and flags")
			decoded, err := ParseFileCapabilities(updated)
			require.NoError(t, err)
			require.Equal(t, uint32(100000), decoded.RootID)
			require.Equal(t, tc.input, hex.EncodeToString(input))
		})
	}
}

func TestParseFileCapabilitiesInvalid(t *testing.T) {
	for name, input := range map[string]string{
		"empty":            "",
		"short-header":     "010000",
		"unknown-revision": "01000004",
		"short-revision1":  "0100000100000000",
		"long-revision1":   "01000001000000000000000000",
		"short-revision2":  "01000002000000000000000000000000000000",
		"long-revision2":   "010000020000000000000000000000000000000000",
		"short-revision3":  "0100000300000000000000000000000000000000",
		"long-revision3":   "01000003000000000000000000000000000000000000000000",
		"reserved-flags":   "0300000200000000000000000000000000000000",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := hex.DecodeString(input)
			require.NoError(t, err)
			_, err = ParseFileCapabilities(data)
			require.Error(t, err)
		})
	}
}
