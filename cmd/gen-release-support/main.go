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
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/semver"
)

const (
	beginMarker = `<!-- release-support:begin (generated from releases/support.toml by "make release-support"; do not edit) -->`
	endMarker   = `<!-- release-support:end -->`

	githubURL    = "https://github.com/"
	repoURL      = githubURL + "containerd/containerd"
	tagURLPrefix = repoURL + "/releases/tag/"

	monthLayout  = "2006-01"
	dayDisplay   = "January 2, 2006"
	monthDisplay = "January 2006"
)

func main() {
	supportPath := flag.String("support", "releases/support.toml", "release support data")
	docPath := flag.String("doc", "RELEASES.md", "markdown file containing the generated table")
	asOf := flag.String("date", time.Now().UTC().Format(time.DateOnly), "reference date (YYYY-MM-DD) for release status")
	check := flag.Bool("check", false, "fail if the table is out of date instead of rewriting it")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %q\n", flag.Args())
		os.Exit(2)
	}

	if err := run(*supportPath, *docPath, *asOf, *check, listTags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func listTags() ([]string, error) {
	out, err := exec.Command("git", "ls-remote", "--tags", "--refs", repoURL+".git", "refs/tags/v*", "refs/tags/[0-9]*").Output()
	if err != nil {
		if eerr, ok := errors.AsType[*exec.ExitError](err); ok {
			err = fmt.Errorf("%w: %s", err, bytes.TrimSpace(eerr.Stderr))
		}
		return nil, fmt.Errorf("listing %s tags for the release links needs network access: %w", repoURL, err)
	}
	var tags []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			tags = append(tags, strings.TrimPrefix(ref, "refs/tags/"))
		}
	}
	return tags, nil
}

func run(supportPath, docPath, asOf string, check bool, listTags func() ([]string, error)) error {
	today, err := time.Parse(time.DateOnly, asOf)
	if err != nil {
		return fmt.Errorf("-date: want YYYY-MM-DD, got %q", asOf)
	}
	data, err := os.ReadFile(supportPath)
	if err != nil {
		return err
	}
	releases, err := parseReleases(data)
	if err != nil {
		return fmt.Errorf("%s: %w", supportPath, err)
	}
	doc, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}
	tags, err := listTags()
	if err != nil {
		return err
	}
	if err := resolveLinks(releases, tags); err != nil {
		return fmt.Errorf("%s: %w", supportPath, err)
	}
	updated, err := splice(string(doc), renderTable(releases, today))
	if err != nil {
		return fmt.Errorf("%s: %w", docPath, err)
	}

	if check {
		if updated != string(doc) {
			return fmt.Errorf("%s is out of date with %s as of %s; run \"make release-support\" to regenerate it",
				docPath, supportPath, asOf)
		}
		return nil
	}
	return os.WriteFile(docPath, []byte(updated), 0o644)
}

type date struct {
	first     time.Time
	monthOnly bool
}

func (d *date) UnmarshalText(text []byte) error {
	s := string(text)
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		*d = date{first: t}
		return nil
	}
	if t, err := time.Parse(monthLayout, s); err == nil {
		*d = date{first: t, monthOnly: true}
		return nil
	}
	return fmt.Errorf("invalid date %q: want YYYY-MM-DD or YYYY-MM", s)
}

func (d date) firstUnsupportedDay() time.Time {
	if d.monthOnly {
		return d.first.AddDate(0, 1, 0)
	}
	return d.first
}

func (d date) String() string {
	if d.monthOnly {
		return d.first.Format(monthDisplay)
	}
	return d.first.Format(dayDisplay)
}

type status string

const (
	statusPlanned   status = "Planned"
	statusActive    status = "Active"
	statusLTS       status = "LTS"
	statusEndOfLife status = "End of Life"
)

type release struct {
	Version     string   `toml:"version"`
	Start       date     `toml:"start"`
	EndOfLife   *date    `toml:"end_of_life"`
	LTS         bool     `toml:"lts"`
	Owners      []string `toml:"owners"`
	UntaggedURL string   `toml:"untagged_url"`
	Note        string   `toml:"note"`

	link string
}

func (r release) status(today time.Time) status {
	switch {
	case today.Before(r.Start.first):
		return statusPlanned
	case r.EndOfLife == nil, !today.Before(r.EndOfLife.firstUnsupportedDay()):
		return statusEndOfLife
	case r.LTS:
		return statusLTS
	default:
		return statusActive
	}
}

func (r release) semver() string {
	return "v" + r.Version
}

func parseReleases(data []byte) ([]release, error) {
	var file struct {
		Releases []release `toml:"release"`
	}
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&file); err != nil {
		if serr, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return nil, errors.New(serr.String())
		}
		if derr, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, column := derr.Position()
			return nil, fmt.Errorf("row %d column %d: %w", row, column, err)
		}
		return nil, err
	}

	releases := file.Releases
	for i, r := range releases {
		if err := r.validate(); err != nil {
			return nil, fmt.Errorf("release %d: %w", i+1, err)
		}
		if i > 0 && semver.Compare(releases[i-1].semver(), r.semver()) >= 0 {
			return nil, fmt.Errorf("release %d: versions must be unique and ascending, but %s follows %s",
				i+1, r.Version, releases[i-1].Version)
		}
	}
	return releases, nil
}

func (r release) validate() error {
	if semver.MajorMinor(r.semver()) != r.semver() {
		return fmt.Errorf("invalid version %q: want MAJOR.MINOR", r.Version)
	}
	if r.Start.first.IsZero() {
		return errors.New("missing start")
	}
	if r.EndOfLife == nil && semver.Major(r.semver()) != "v0" {
		return errors.New("missing end_of_life")
	}
	if r.EndOfLife != nil && !r.Start.first.Before(r.EndOfLife.firstUnsupportedDay()) {
		return fmt.Errorf("start %s is not before end_of_life %s", r.Start, r.EndOfLife)
	}
	return nil
}

func resolveLinks(releases []release, tags []string) error {
	type version struct{ tag, semver string }
	latest := make(map[string]version)
	for _, t := range tags {
		v := "v" + strings.TrimPrefix(t, "v")
		if semver.Canonical(v) != v || semver.Prerelease(v) != "" {
			continue
		}
		minor := strings.TrimPrefix(semver.MajorMinor(v), "v")
		if cur, ok := latest[minor]; !ok || semver.Compare(v, cur.semver) > 0 {
			latest[minor] = version{t, v}
		}
	}

	for i, r := range releases {
		if v, ok := latest[r.Version]; ok {
			releases[i].link = tagURLPrefix + v.tag
		} else if r.UntaggedURL != "" {
			releases[i].link = r.UntaggedURL
		} else {
			return fmt.Errorf("release %s: no stable %s.Z tag and no untagged_url", r.Version, r.semver())
		}
	}
	return nil
}

func renderTable(releases []release, today time.Time) string {
	rows := [][]string{{"Release", "Status", "Start", "End of Life", "Owners"}}
	var footnotes []string
	for _, r := range releases {
		s := r.status(today)
		eol := "-"
		if r.EndOfLife != nil {
			eol = r.EndOfLife.String()
		}
		if r.Note != "" {
			n := len(footnotes) + 1
			eol += strings.Repeat("*", n)
			footnotes = append(footnotes, strings.Repeat(`\*`, n)+" "+r.Note)
		}
		var owners string
		if s != statusEndOfLife {
			owners = ownerLinks(r.Owners)
		}
		cells := []string{fmt.Sprintf("[%s](%s)", r.Version, r.link), string(s), r.Start.String(), eol}
		if s == statusPlanned {
			for i, c := range cells {
				cells[i] = "_" + c + "_"
			}
		}
		rows = append(rows, append(cells, owners))
	}

	var b strings.Builder
	b.WriteString(formatTable(rows))
	for _, f := range footnotes {
		fmt.Fprintf(&b, "\n%s\n", f)
	}
	return b.String()
}

func ownerLinks(owners []string) string {
	links := make([]string, len(owners))
	for i, o := range owners {
		if strings.Contains(o, "/") {
			links[i] = "@" + o
		} else {
			links[i] = fmt.Sprintf("[@%s](%s%s)", o, githubURL, o)
		}
	}
	return strings.Join(links, ", ")
}

func formatTable(rows [][]string) string {
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	separator := make([]string, len(widths))
	for i, w := range widths {
		separator[i] = strings.Repeat("-", w)
	}

	var b strings.Builder
	for _, row := range append([][]string{rows[0], separator}, rows[1:]...) {
		b.WriteString("|")
		for i, c := range row {
			fmt.Fprintf(&b, " %-*s |", widths[i], c)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func splice(doc, generated string) (string, error) {
	if strings.Count(doc, beginMarker) != 1 || strings.Count(doc, endMarker) != 1 {
		return "", fmt.Errorf("want exactly one %s line and one %s line", beginMarker, endMarker)
	}
	before, rest, _ := strings.Cut(doc, beginMarker)
	_, after, ok := strings.Cut(rest, endMarker)
	if !ok {
		return "", fmt.Errorf("want %s after %s", endMarker, beginMarker)
	}
	return before + beginMarker + "\n\n" + generated + "\n" + endMarker + after, nil
}
