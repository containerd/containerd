//go:build linux
// +build linux

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

package apparmor

import (
	"testing"
)

type versionExpected struct {
	output  string
	version int
}

func TestParseVersion(t *testing.T) {
	versions := []versionExpected{
		{
			output: `AppArmor parser version 2.10
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2012 Canonical Ltd.
`,
			version: 210000,
		},
		{
			output: `AppArmor parser version 2.8
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2012 Canonical Ltd.
`,
			version: 208000,
		},
		{
			output: `AppArmor parser version 2.20
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2012 Canonical Ltd.
`,
			version: 220000,
		},
		{
			output: `AppArmor parser version 2.05
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2012 Canonical Ltd.
`,
			version: 205000,
		},
		{
			output: `AppArmor parser version 2.9.95
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2012 Canonical Ltd.
`,
			version: 209095,
		},
		{
			output: `AppArmor parser version 3.14.159
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2012 Canonical Ltd.
`,
			version: 314159,
		},
		{
			output: `AppArmor parser version 3.0.0-beta1
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2018 Canonical Ltd.
`,
			version: 300000,
		},
		{
			output: `AppArmor parser version 3.0.0-beta1-foo-bar
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2018 Canonical Ltd.
`,
			version: 300000,
		},
		{
			output: `AppArmor parser version 2.7.0~rc2
Copyright (C) 1999-2008 Novell Inc.
Copyright 2009-2018 Canonical Ltd.
`,
			version: 207000,
		},
	}

	for _, v := range versions {
		version, err := parseVersion(v.output)
		if err != nil {
			t.Fatalf("expected error to be nil for %#v, got: %v", v, err)
		}
		if version != v.version {
			t.Fatalf("expected version to be %d, was %d, for: %#v\n", v.version, version, v)
		}
	}
}

func TestDumpDefaultProfile(t *testing.T) {
	if _, err := getVersion(); err != nil {
		t.Skipf("AppArmor not available: %+v", err)
	}
	name := "test-dump-default-profile"
	prof, err := DumpDefaultProfile(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Generated profile %q", name)
	t.Log(prof)
}
