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

package docker

import (
	"reflect"
	"testing"

	"github.com/containerd/containerd/v2/labels"
	"github.com/containerd/containerd/v2/reference"
)

func TestAppendDistributionLabel(t *testing.T) {
	for _, tc := range []struct {
		originLabel string
		repo        string
		expected    string
	}{
		{
			originLabel: "",
			repo:        "",
			expected:    "",
		},
		{
			originLabel: "",
			repo:        "library/busybox",
			expected:    "library/busybox",
		},
		{
			originLabel: "library/busybox",
			repo:        "library/busybox",
			expected:    "library/busybox",
		},
		// remove the duplicate one in origin
		{
			originLabel: "library/busybox,library/redis,library/busybox",
			repo:        "library/alpine",
			expected:    "library/alpine,library/busybox,library/redis",
		},
		// remove the empty repo
		{
			originLabel: "library/busybox,library/redis,library/busybox",
			repo:        "",
			expected:    "library/busybox,library/redis",
		},
		{
			originLabel: "library/busybox,library/redis,library/busybox",
			repo:        "library/redis",
			expected:    "library/busybox,library/redis",
		},
	} {
		if got := appendDistributionSourceLabel(tc.originLabel, tc.repo); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("expected %v, but got %v", tc.expected, got)
		}
	}
}

func TestDistributionSourceLabelKey(t *testing.T) {
	expected := labels.LabelDistributionSource + ".testsource"
	if got := distributionSourceLabelKey("testsource"); !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, but got %v", expected, got)
	}
}

func TestCommonPrefixComponents(t *testing.T) {
	for _, tc := range []struct {
		components []string
		target     string
		expected   int
	}{
		{
			components: []string{"foo"},
			target:     "foo/bar",
			expected:   1,
		},
		{
			components: []string{"bar"},
			target:     "foo/bar",
			expected:   0,
		},
		{
			components: []string{"foo", "bar"},
			target:     "foo/bar",
			expected:   2,
		},
	} {
		if got := commonPrefixComponents(tc.components, tc.target); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("expected %v, but got %v", tc.expected, got)
		}
	}
}

func TestSelectRepositoryMountCandidate(t *testing.T) {
	for _, tc := range []struct {
		refspec  reference.Spec
		source   map[string]string
		expected string
	}{
		{
			refspec:  reference.Spec{},
			source:   map[string]string{"": ""},
			expected: "",
		},
		{
			refspec:  reference.Spec{Locator: "user@host/path"},
			source:   map[string]string{labels.LabelDistributionSource + ".host": "foo,path,bar"},
			expected: "bar",
		},
		{
			refspec:  reference.Spec{Locator: "user@host/path"},
			source:   map[string]string{labels.LabelDistributionSource + ".host": "foo,bar,path"},
			expected: "bar",
		},
	} {
		if got := selectRepositoryMountCandidate(tc.refspec, tc.source); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("expected %v, but got %v", tc.expected, got)
		}
	}
}
