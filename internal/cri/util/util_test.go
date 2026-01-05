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

package util

import (
	"fmt"
	"strings"
	"testing"

	crilabels "github.com/containerd/containerd/v2/internal/cri/labels"
	"github.com/containerd/errdefs/pkg/errgrpc"
	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestPassThroughAnnotationsFilter(t *testing.T) {
	for _, test := range []struct {
		desc                   string
		podAnnotations         map[string]string
		runtimePodAnnotations  []string
		passthroughAnnotations map[string]string
	}{
		{
			desc:                   "should support direct match",
			podAnnotations:         map[string]string{"c": "d", "d": "e"},
			runtimePodAnnotations:  []string{"c"},
			passthroughAnnotations: map[string]string{"c": "d"},
		},
		{
			desc: "should support wildcard match",
			podAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"z":    "o",
				"y.ca": "b",
				"y":    "b",
			},
			runtimePodAnnotations: []string{"*.f", "z*g", "y.c*"},
			passthroughAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"y.ca": "b",
			},
		},
		{
			desc: "should support wildcard match all",
			podAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"z":    "o",
				"y.ca": "b",
				"y":    "b",
			},
			runtimePodAnnotations: []string{"*"},
			passthroughAnnotations: map[string]string{
				"t.f":  "j",
				"z.g":  "o",
				"z":    "o",
				"y.ca": "b",
				"y":    "b",
			},
		},
		{
			desc: "should support match including path separator",
			podAnnotations: map[string]string{
				"matchend.com/end":    "1",
				"matchend.com/end1":   "2",
				"matchend.com/1end":   "3",
				"matchmid.com/mid":    "4",
				"matchmid.com/mi1d":   "5",
				"matchmid.com/mid1":   "6",
				"matchhead.com/head":  "7",
				"matchhead.com/1head": "8",
				"matchhead.com/head1": "9",
				"matchall.com/abc":    "10",
				"matchall.com/def":    "11",
				"end/matchend":        "12",
				"end1/matchend":       "13",
				"1end/matchend":       "14",
				"mid/matchmid":        "15",
				"mi1d/matchmid":       "16",
				"mid1/matchmid":       "17",
				"head/matchhead":      "18",
				"1head/matchhead":     "19",
				"head1/matchhead":     "20",
				"abc/matchall":        "21",
				"def/matchall":        "22",
				"match1/match2":       "23",
				"nomatch/nomatch":     "24",
			},
			runtimePodAnnotations: []string{
				"matchend.com/end*",
				"matchmid.com/mi*d",
				"matchhead.com/*head",
				"matchall.com/*",
				"end*/matchend",
				"mi*d/matchmid",
				"*head/matchhead",
				"*/matchall",
				"match*/match*",
			},
			passthroughAnnotations: map[string]string{
				"matchend.com/end":    "1",
				"matchend.com/end1":   "2",
				"matchmid.com/mid":    "4",
				"matchmid.com/mi1d":   "5",
				"matchhead.com/head":  "7",
				"matchhead.com/1head": "8",
				"matchall.com/abc":    "10",
				"matchall.com/def":    "11",
				"end/matchend":        "12",
				"end1/matchend":       "13",
				"mid/matchmid":        "15",
				"mi1d/matchmid":       "16",
				"head/matchhead":      "18",
				"1head/matchhead":     "19",
				"abc/matchall":        "21",
				"def/matchall":        "22",
				"match1/match2":       "23",
			},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			passthroughAnnotations := GetPassthroughAnnotations(test.podAnnotations, test.runtimePodAnnotations)
			assert.Equal(t, test.passthroughAnnotations, passthroughAnnotations)
		})
	}
}

func TestBuildLabels(t *testing.T) {
	imageConfigLabels := map[string]string{
		"a":          "z",
		"d":          "y",
		"long-label": strings.Repeat("example", 10000),
	}
	configLabels := map[string]string{
		"a": "b",
		"c": "d",
	}
	newLabels := BuildLabels(configLabels, imageConfigLabels, crilabels.ContainerKindSandbox)
	assert.Len(t, newLabels, 4)
	assert.Equal(t, "b", newLabels["a"])
	assert.Equal(t, "d", newLabels["c"])
	assert.Equal(t, "y", newLabels["d"])
	assert.Equal(t, crilabels.ContainerKindSandbox, newLabels[crilabels.ContainerKindLabel])
	assert.NotContains(t, newLabels, "long-label")

	newLabels["a"] = "e"
	assert.Empty(t, configLabels[crilabels.ContainerKindLabel], "should not add new labels into original label")
	assert.Equal(t, "b", configLabels["a"], "change in new labels should not affect original label")
}

func TestGenerateUserString(t *testing.T) {
	type testcase struct {
		// the name of the test case
		name string

		u        string
		uid, gid *runtime.Int64Value

		result        string
		expectedError bool
	}
	testcases := []testcase{
		{
			name:   "Empty",
			result: "",
		},
		{
			name:   "Username Only",
			u:      "testuser",
			result: "testuser",
		},
		{
			name:   "Username, UID",
			u:      "testuser",
			uid:    &runtime.Int64Value{Value: 1},
			result: "testuser",
		},
		{
			name:   "Username, UID, GID",
			u:      "testuser",
			uid:    &runtime.Int64Value{Value: 1},
			gid:    &runtime.Int64Value{Value: 10},
			result: "testuser:10",
		},
		{
			name:   "Username, GID",
			u:      "testuser",
			gid:    &runtime.Int64Value{Value: 10},
			result: "testuser:10",
		},
		{
			name:   "UID only",
			uid:    &runtime.Int64Value{Value: 1},
			result: "1",
		},
		{
			name:   "UID, GID",
			uid:    &runtime.Int64Value{Value: 1},
			gid:    &runtime.Int64Value{Value: 10},
			result: "1:10",
		},
		{
			name:          "GID only",
			gid:           &runtime.Int64Value{Value: 10},
			result:        "",
			expectedError: true,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := GenerateUserString(tc.u, tc.uid, tc.gid)
			if tc.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.result, r)
		})
	}
}

func TestIsShimTTRPCClosed(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ttrpc closed error text",
			err:      fmt.Errorf("ttrpc: closed"),
			expected: true,
		},
		{
			name:     "ttrpc closed error",
			err:      ttrpc.ErrClosed,
			expected: true,
		},
		{
			name:     "wrapped ttrpc closed error",
			err:      fmt.Errorf("some context: %w", ttrpc.ErrClosed),
			expected: true,
		},
		{
			name:     "non-ttrpc error",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, IsShimTTRPCClosed(errgrpc.ToNative(tt.err)))
		})
	}
}
