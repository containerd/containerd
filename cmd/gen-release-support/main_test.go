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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.DateOnly, s)
	require.NoError(t, err)
	return d
}

func mustDate(t *testing.T, s string) date {
	t.Helper()
	var d date
	require.NoError(t, d.UnmarshalText([]byte(s)))
	return d
}

func TestStatus(t *testing.T) {
	for _, tc := range []struct {
		name             string
		start, endOfLife string
		lts              bool
		today            string
		want             status
	}{
		{"day before start", "2025-11-05", "2026-11-06", false, "2025-11-04", statusPlanned},
		{"on start", "2025-11-05", "2026-11-06", false, "2025-11-05", statusActive},
		{"day before month start", "2027-01", "2027-09-06", false, "2026-12-31", statusPlanned},
		{"first day of month start", "2027-01", "2027-09-06", false, "2027-01-01", statusActive},
		{"day before end of life", "2025-11-05", "2026-11-06", false, "2026-11-05", statusActive},
		{"on end of life", "2025-11-05", "2026-11-06", false, "2026-11-06", statusEndOfLife},
		{"last day of end of life month", "2024-11-05", "2027-03", false, "2027-03-31", statusActive},
		{"first day after end of life month", "2024-11-05", "2027-03", false, "2027-04-01", statusEndOfLife},
		{"lts within support", "2024-11-05", "2027-03", true, "2026-11-04", statusLTS},
		{"lts after end of life", "2022-02-15", "2025-08-23", true, "2026-11-04", statusEndOfLife},
		{"lts before start", "2027-01-06", "2029-01-06", true, "2026-11-04", statusPlanned},
		{"missing end of life", "2015-12-04", "", false, "2026-11-04", statusEndOfLife},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := release{Start: mustDate(t, tc.start), LTS: tc.lts}
			if tc.endOfLife != "" {
				eol := mustDate(t, tc.endOfLife)
				r.EndOfLife = &eol
			}
			assert.Equal(t, tc.want, r.status(day(t, tc.today)), "status on %s", tc.today)
		})
	}
}

const fixture = `
[[release]]
version = "1.0"
start = "2020-01-02"
end_of_life = "2021-01-02"
owners = ["alice"]

[[release]]
version = "1.1"
start = "2020-06"
end_of_life = "2027-03"
lts = true
owners = ["alice", "bob"]
note = "First note."

[[release]]
version = "2.0"
start = "2027-01-06"
end_of_life = "2027-09-06"
owners = ["org/team"]
untagged_url = "https://example.com/m/1"
note = "Second [note](https://example.com)."
`

var fixtureTags = []string{"v1.0.0", "v1.0.3", "v1.1.0", "v1.1.2", "v2.0.0-rc.0"}

func tagsFunc(tags []string) func() ([]string, error) {
	return func() ([]string, error) { return tags, nil }
}

const fixtureTable = `| Release                                                             | Status      | Start             | End of Life           | Owners                                                             |
| ------------------------------------------------------------------- | ----------- | ----------------- | --------------------- | ------------------------------------------------------------------ |
| [1.0](https://github.com/containerd/containerd/releases/tag/v1.0.3) | End of Life | January 2, 2020   | January 2, 2021       |                                                                    |
| [1.1](https://github.com/containerd/containerd/releases/tag/v1.1.2) | LTS         | June 2020         | March 2027*           | [@alice](https://github.com/alice), [@bob](https://github.com/bob) |
| _[2.0](https://example.com/m/1)_                                    | _Planned_   | _January 6, 2027_ | _September 6, 2027**_ | @org/team                                                          |

\* First note.

\*\* Second [note](https://example.com).
`

func TestRenderTable(t *testing.T) {
	releases, err := parseReleases([]byte(fixture))
	require.NoError(t, err)
	require.NoError(t, resolveLinks(releases, fixtureTags))
	assert.Equal(t, fixtureTable, renderTable(releases, day(t, "2026-11-04")))
}

func TestResolveLinks(t *testing.T) {
	const tagURL = "https://github.com/containerd/containerd/releases/tag/"
	for _, tc := range []struct {
		name     string
		untagged string
		tags     []string
		want     string
		wantErr  string
	}{
		{"highest patch", "", []string{"v1.1.0", "v1.1.10", "v1.1.9", "v1.2.0", "v1.0.11"}, tagURL + "v1.1.10", ""},
		{"prerelease ignored", "", []string{"v1.1.1", "v1.1.2-rc.0", "v1.1.2-beta.1"}, tagURL + "v1.1.1", ""},
		{"non-canonical ignored", "", []string{"v1.1", "v1.1.3+build", "vv1.1.4", "v1.1.2"}, tagURL + "v1.1.2", ""},
		{"tag without v", "", []string{"1.1.4", "1.1.10", "1.1.5-rc.0"}, tagURL + "1.1.10", ""},
		{"tag over untagged url", "https://example.com/m/1", []string{"v1.1.2"}, tagURL + "v1.1.2", ""},
		{"untagged url without tags", "https://example.com/m/1", nil, "https://example.com/m/1", ""},
		{"no stable tag or untagged url", "", []string{"v1.1", "v1.1.0-rc.0", "v1.10.0", "v11.1.0"}, "", "release 1.1: no stable v1.1.Z tag and no untagged_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releases := []release{{Version: "1.1", UntaggedURL: tc.untagged}}
			err := resolveLinks(releases, tc.tags)
			if tc.wantErr != "" {
				assert.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, releases[0].link)
		})
	}
}

func TestParseReleasesErrors(t *testing.T) {
	const valid = `
[[release]]
version = "1.9"
start = "2021-01-02"
end_of_life = "2021-01"

[[release]]
version = "1.10"
start = "2021-01"
end_of_life = "2022-01"
`
	_, err := parseReleases([]byte(valid))
	require.NoError(t, err, "valid input")
	_, err = parseReleases([]byte("[[release]]\nversion = \"0.9\"\nstart = \"2016-01\"\n"))
	require.NoError(t, err, "0.x release without end_of_life")
	_, err = parseReleases([]byte("[[release]]\nversion = \"1.11\"\nstart = \"2021-03-10\"\nend_of_life = \"2021-03\"\n"))
	require.NoError(t, err, "start in its end_of_life month")

	for _, tc := range []struct {
		name, old, new, wantErr string
	}{
		{"unknown key", `start = "2021-01"`, `start = "2021-01"` + "\nlst = true", "lst"},
		{"invalid month", `"2021-01-02"`, `"2021-13-02"`, `invalid date "2021-13-02"`},
		{"invalid day", `"2021-01-02"`, `"2021-02-30"`, `invalid date "2021-02-30"`},
		{"unpadded date", `"2021-01-02"`, `"2021-1-2"`, `invalid date "2021-1-2"`},
		{"slash date", `"2021-01-02"`, `"2021/01/02"`, `invalid date "2021/01/02"`},
		{"missing version", `version = "1.10"`, ``, `release 2: invalid version ""`},
		{"missing start", `start = "2021-01"`, ``, "release 2: missing start"},
		{"missing end of life", `end_of_life = "2022-01"`, ``, "release 2: missing end_of_life"},
		{"end of life not after start", `end_of_life = "2021-01"`, `end_of_life = "2019-12"`, "release 1: start January 2, 2021 is not before end_of_life December 2019"},
		{"invalid version", `"1.10"`, `"v1.10"`, `invalid version "v1.10"`},
		{"patch version", `"1.10"`, `"1.10.0"`, `invalid version "1.10.0"`},
		{"leading zero version", `"1.10"`, `"1.010"`, `invalid version "1.010"`},
		{"unordered versions", `"1.10"`, `"1.8"`, "release 2: versions must be unique and ascending, but 1.8 follows 1.9"},
		{"duplicate versions", `"1.10"`, `"1.9"`, "release 2: versions must be unique and ascending, but 1.9 follows 1.9"},
		{"non-string date", `"2021-01-02"`, `2021-01-02`, "row 4 column 9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(valid, tc.old, tc.new, 1)
			require.NotEqual(t, valid, input, "mutation %q did not apply", tc.old)
			_, err := parseReleases([]byte(input))
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRun(t *testing.T) {
	const asOf = "2026-11-04"
	dir := t.TempDir()
	support := filepath.Join(dir, "support.toml")
	doc := filepath.Join(dir, "RELEASES.md")
	require.NoError(t, os.WriteFile(support, []byte(fixture), 0o644))
	stale := "# Releases\n\n" + beginMarker + "\nold table\n" + endMarker + "\n\n## After\n"
	require.NoError(t, os.WriteFile(doc, []byte(stale), 0o644))
	readDoc := func() string {
		data, err := os.ReadFile(doc)
		require.NoError(t, err)
		return string(data)
	}

	tags := tagsFunc(fixtureTags)
	require.ErrorContains(t, run(support, doc, asOf, true, tags), "make release-support", "check on stale doc")
	require.Equal(t, stale, readDoc(), "check modified the doc")

	require.NoError(t, run(support, doc, asOf, false, tags))
	want := "# Releases\n\n" + beginMarker + "\n\n" + fixtureTable + "\n" + endMarker + "\n\n## After\n"
	require.Equal(t, want, readDoc(), "generated doc")

	assert.NoError(t, run(support, doc, asOf, true, tags), "check after generate")
	assert.Error(t, run(support, doc, "2027-01-06", true, tags), "check on the day 2.0 leaves Planned")
	assert.ErrorContains(t, run(support, doc, asOf, true, tagsFunc(append(fixtureTags, "v1.1.3"))), "make release-support", "check after a new patch tag")
	assert.NoError(t, run(support, doc, asOf, false, tags))
	assert.Equal(t, want, readDoc(), "regenerating an up-to-date doc changed it")
	noTags := func() ([]string, error) {
		t.Error("listed tags before validating -date")
		return fixtureTags, nil
	}
	assert.ErrorContains(t, run(support, doc, "2026-11", false, noTags), "-date: want YYYY-MM-DD")
	assert.ErrorContains(t, run(support, doc, asOf, false, tagsFunc([]string{"v1.1.2"})), "release 1.0: no stable v1.0.Z tag and no untagged_url")
	assert.Equal(t, want, readDoc(), "failed generate changed the doc")

	require.NoError(t, os.WriteFile(doc, []byte("# Releases\n"), 0o644))
	assert.Error(t, run(support, doc, asOf, false, tags), "generate without markers")
}
