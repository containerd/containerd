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

package restart

import (
	"testing"

	"github.com/containerd/containerd/v2"
	"github.com/stretchr/testify/assert"
)

func TestNewRestartPolicy(t *testing.T) {
	tests := []struct {
		policy string
		want   *Policy
	}{
		{
			policy: "unknow",
			want:   nil,
		},
		{
			policy: "",
			want:   &Policy{name: "always"},
		},
		{
			policy: "always",
			want:   &Policy{name: "always"},
		},
		{
			policy: "always:3",
			want:   nil,
		},
		{
			policy: "on-failure",
			want:   &Policy{name: "on-failure"},
		},
		{
			policy: "on-failure:10",
			want: &Policy{
				name:              "on-failure",
				maximumRetryCount: 10,
			},
		},
		{
			policy: "unless-stopped",
			want: &Policy{
				name: "unless-stopped",
			},
		},
	}

	for _, testCase := range tests {
		result, _ := NewPolicy(testCase.policy)
		assert.Equal(t, testCase.want, result)
	}
}

func TestRestartPolicyToString(t *testing.T) {
	tests := []struct {
		policy string
		want   string
	}{
		{
			policy: "",
			want:   "always",
		},
		{
			policy: "always",
			want:   "always",
		},
		{
			policy: "on-failure",
			want:   "on-failure",
		},
		{
			policy: "on-failure:10",
			want:   "on-failure:10",
		},
		{
			policy: "unless-stopped",
			want:   "unless-stopped",
		},
	}

	for _, testCase := range tests {
		policy, err := NewPolicy(testCase.policy)
		if err != nil {
			t.Fatal(err)
		}
		result := policy.String()
		assert.Equal(t, testCase.want, result)
	}
}

func TestRestartPolicyReconcile(t *testing.T) {
	tests := []struct {
		status containerd.Status
		labels map[string]string
		want   bool
	}{
		{
			status: containerd.Status{
				Status: containerd.Stopped,
			},
			labels: map[string]string{
				PolicyLabel: "always",
			},
			want: true,
		},
		{
			status: containerd.Status{
				Status: containerd.Unknown,
			},
			labels: map[string]string{
				PolicyLabel: "always",
			},
			want: true,
		},
		{
			status: containerd.Status{
				Status: containerd.Stopped,
			},
			labels: map[string]string{
				PolicyLabel: "on-failure:10",
				CountLabel:  "1",
			},
			want: false,
		},
		{
			status: containerd.Status{
				Status:     containerd.Unknown,
				ExitStatus: 1,
			},
			// test without count label
			labels: map[string]string{
				PolicyLabel: "on-failure:10",
			},
			want: true,
		},
		{
			status: containerd.Status{
				Status:     containerd.Unknown,
				ExitStatus: 1,
			},
			// test without valid count label
			labels: map[string]string{
				PolicyLabel: "on-failure:10",
				CountLabel:  "invalid",
			},
			want: false,
		},
		{
			status: containerd.Status{
				Status:     containerd.Unknown,
				ExitStatus: 1,
			},
			labels: map[string]string{
				PolicyLabel: "on-failure:10",
				CountLabel:  "1",
			},
			want: true,
		},
		{
			status: containerd.Status{
				Status:     containerd.Unknown,
				ExitStatus: 1,
			},
			labels: map[string]string{
				PolicyLabel: "on-failure:3",
				CountLabel:  "3",
			},
			want: false,
		},
		{
			status: containerd.Status{
				Status: containerd.Unknown,
			},
			labels: map[string]string{
				PolicyLabel: "unless-stopped",
			},
			want: true,
		},
		{
			status: containerd.Status{
				Status: containerd.Stopped,
			},
			labels: map[string]string{
				PolicyLabel: "unless-stopped",
			},
			want: true,
		},
		{
			status: containerd.Status{
				Status: containerd.Stopped,
			},
			labels: map[string]string{
				PolicyLabel:            "unless-stopped",
				ExplicitlyStoppedLabel: "true",
			},
			want: false,
		},
	}
	for _, testCase := range tests {
		result := Reconcile(testCase.status, testCase.labels)
		assert.Equal(t, testCase.want, result, testCase)
	}
}
