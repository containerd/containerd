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

package snapshots

import (
	"encoding/json"
	"testing"
)

func TestKindString(t *testing.T) {
	tests := []struct {
		kind     Kind
		expected string
	}{
		{KindUnknown, "Unknown"},
		{KindView, "View"},
		{KindActive, "Active"},
		{KindCommitted, "Committed"},
		{KindSnapshot, "Snapshot"},
		{Kind(99), "Unknown"},
	}
	for _, tc := range tests {
		if got := tc.kind.String(); got != tc.expected {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.kind, got, tc.expected)
		}
	}
}

func TestParseKind(t *testing.T) {
	tests := []struct {
		input    string
		expected Kind
	}{
		{"view", KindView},
		{"View", KindView},
		{"VIEW", KindView},
		{"active", KindActive},
		{"committed", KindCommitted},
		{"snapshot", KindSnapshot},
		{"Snapshot", KindSnapshot},
		{"SNAPSHOT", KindSnapshot},
		{"unknown", KindUnknown},
		{"invalid", KindUnknown},
		{"", KindUnknown},
	}
	for _, tc := range tests {
		if got := ParseKind(tc.input); got != tc.expected {
			t.Errorf("ParseKind(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestKindJSON(t *testing.T) {
	tests := []struct {
		kind Kind
		json string
	}{
		{KindView, `"View"`},
		{KindActive, `"Active"`},
		{KindCommitted, `"Committed"`},
		{KindSnapshot, `"Snapshot"`},
		{KindUnknown, `"Unknown"`},
	}
	for _, tc := range tests {
		b, err := json.Marshal(tc.kind)
		if err != nil {
			t.Fatalf("Marshal(%v) failed: %v", tc.kind, err)
		}
		if string(b) != tc.json {
			t.Errorf("Marshal(%v) = %s, want %s", tc.kind, b, tc.json)
		}

		var got Kind
		if err := json.Unmarshal([]byte(tc.json), &got); err != nil {
			t.Fatalf("Unmarshal(%s) failed: %v", tc.json, err)
		}
		if got != tc.kind {
			t.Errorf("Unmarshal(%s) = %v, want %v", tc.json, got, tc.kind)
		}
	}
}
