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

package platforms

import (
	"errors"
	"runtime"
	"testing"

	"github.com/containerd/containerd/errdefs"
)

func TestCPUVariant(t *testing.T) {
	if !isArmArch(runtime.GOARCH) {
		t.Skip("only relevant on linux/arm")
	}

	variants := []string{"v8", "v7", "v6", "v5", "v4", "v3"}

	p, err := getCPUVariant()
	if err != nil {
		t.Fatalf("Error getting CPU variant: %v", err)
		return
	}

	for _, variant := range variants {
		if p == variant {
			t.Logf("got valid variant as expected: %#v = %#v", p, variant)
			return
		}
	}

	t.Fatalf("could not get valid variant as expected: %v", variants)
}

func TestGetCPUVariantFromArch(t *testing.T) {

	for _, testcase := range []struct {
		name        string
		input       string
		output      string
		expectedErr error
	}{
		{
			name:        "Test aarch64",
			input:       "aarch64",
			output:      "8",
			expectedErr: nil,
		},
		{
			name:        "Test Armv8 with capital",
			input:       "Armv8",
			output:      "8",
			expectedErr: nil,
		},
		{
			name:        "Test armv7",
			input:       "armv7",
			output:      "7",
			expectedErr: nil,
		},
		{
			name:        "Test armv6",
			input:       "armv6",
			output:      "6",
			expectedErr: nil,
		},
		{
			name:        "Test armv5",
			input:       "armv5",
			output:      "5",
			expectedErr: nil,
		},
		{
			name:        "Test armv4",
			input:       "armv4",
			output:      "4",
			expectedErr: nil,
		},
		{
			name:        "Test armv3",
			input:       "armv3",
			output:      "3",
			expectedErr: nil,
		},
		{
			name:        "Test unknown input",
			input:       "armv9",
			output:      "unknown",
			expectedErr: nil,
		},
		{
			name:        "Test invalid input which doesn't start with armv",
			input:       "armxxxx",
			output:      "",
			expectedErr: errdefs.ErrInvalidArgument,
		},
		{
			name:        "Test invalid input whose length is less than 5",
			input:       "armv",
			output:      "",
			expectedErr: errdefs.ErrInvalidArgument,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			t.Logf("input: %v", testcase.input)

			variant, err := getCPUVariantFromArch(testcase.input)

			if err == nil {
				if testcase.expectedErr != nil {
					t.Fatalf("Expect to get error: %v, however no error got", testcase.expectedErr)
				} else {
					if variant != testcase.output {
						t.Fatalf("Expect to get variant: %v, however %v returned", testcase.output, variant)
					}
				}

			} else {
				if !errors.Is(err, testcase.expectedErr) {
					t.Fatalf("Expect to get error: %v, however error %v returned", testcase.expectedErr, err)
				}
			}
		})

	}
}
