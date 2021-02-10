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

package oci

import (
	"context"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func TestAddCaps(t *testing.T) {
	t.Parallel()

	var s specs.Spec

	if err := WithAddedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if !capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d does not contain added cap", i)
		}
	}
}

func TestDropCaps(t *testing.T) {
	t.Parallel()

	var s specs.Spec

	if err := WithAllKnownCapabilities(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}

	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d contains dropped cap", i)
		}
	}

	// Add all capabilities back and drop a different cap.
	if err := WithAllKnownCapabilities(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_FOWNER"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}

	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if capsContain(cl, "CAP_FOWNER") {
			t.Errorf("cap list %d contains dropped cap", i)
		}
		if !capsContain(cl, "CAP_CHOWN") {
			t.Errorf("cap list %d doesn't contain non-dropped cap", i)
		}
	}

	// Drop all duplicated caps.
	if err := WithCapabilities([]string{"CAP_CHOWN", "CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	if err := WithDroppedCapabilities([]string{"CAP_CHOWN"})(context.Background(), nil, nil, &s); err != nil {
		t.Fatal(err)
	}
	for i, cl := range [][]string{
		s.Process.Capabilities.Bounding,
		s.Process.Capabilities.Effective,
		s.Process.Capabilities.Permitted,
		s.Process.Capabilities.Inheritable,
	} {
		if len(cl) != 0 {
			t.Errorf("cap list %d is not empty", i)
		}
	}
}
