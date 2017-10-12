package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/BurntSushi/toml"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const releaseNotes = `Welcome to the release of containerd {{.Version}}!
{{if .PreRelease}}
*This is a pre-release of containerd*
{{- end}}

{{.Preface}}

Please try out the release binaries and report any issues at
https://github.com/containerd/containerd/issues.

{{range  $note := .Notes}}
### {{$note.Title}}

{{$note.Description}}
{{- end}}

### Contributors
{{range $contributor := .Contributors}}
* {{$contributor}}
{{- end}}

### Changes
{{range $change := .Changes}}
* {{$change.Commit}} {{$change.Description}}
{{- end}}

### Dependency Changes

Previous release can be found at [{{.Previous}}](https://github.com/containerd/containerd/releases/tag/{{.Previous}})
{{range $dep := .Dependencies}}
* {{$dep.Previous}} -> {{$dep.Commit}} **{{$dep.Name}}**
{{- end}}
`

const vendorConf = "vendor.conf"

type note struct {
	Title       string `toml:"title"`
	Description string `toml:"description"`
}

type change struct {
	Commit      string `toml:"commit"`
	Description string `toml:"description"`
}

type dependency struct {
	Name     string
	Commit   string
	Previous string
}

type release struct {
	Commit          string            `toml:"commit"`
	Previous        string            `toml:"previous"`
	PreRelease      bool              `toml:"pre_release"`
	Preface         string            `toml:"preface"`
	Notes           map[string]note   `toml:"notes"`
	BreakingChanges map[string]change `toml:"breaking"`
	// generated fields
	Changes      []change
	Contributors []string
	Dependencies []dependency
	Version      string
}

func main() {
	app := cli.NewApp()
	app.Name = "containerd-release"
	app.Description = `release tooling for containerd.

This tool should be ran from the root of the containerd repository for a new release.
`
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "dry,n",
			Usage: "run the release tooling as a dry run to print the release notes to stdout",
		},
	}
	app.Action = func(context *cli.Context) error {
		logrus.Info("Welcome to the containerd release tool...")
		var (
			path = context.Args().First()
			tag  = parseTag(path)
		)
		r, err := loadRelease(path)
		if err != nil {
			return err
		}
		previous, err := getPreviousDeps(r.Previous)
		if err != nil {
			return err
		}
		changes, err := changelog(r.Previous, r.Commit)
		if err != nil {
			return err
		}
		logrus.Infof("creating new release %s with %d new changes...", tag, len(changes))
		rd, err := fileFromRev(r.Commit, vendorConf)
		if err != nil {
			return err
		}
		deps, err := parseDependencies(rd)
		if err != nil {
			return err
		}
		updatedDeps := updatedDeps(previous, deps)
		contributors, err := getContributors(r.Previous, r.Commit)
		if err != nil {
			return err
		}
		// update the release fields with generated data
		r.Contributors = contributors
		r.Dependencies = updatedDeps
		r.Changes = changes
		r.Version = tag

		if context.Bool("dry") {
			t, err := template.New("release-notes").Parse(releaseNotes)
			if err != nil {
				return err
			}
			return t.Execute(os.Stdout, r)
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
}

func loadRelease(path string) (*release, error) {
	var r release
	if _, err := toml.DecodeFile(path, &r); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("please specify the release file as the first argument")
		}
		return nil, err
	}
	return &r, nil
}

func parseTag(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".toml")
}

func parseDependencies(r io.Reader) ([]dependency, error) {
	var deps []dependency
	s := bufio.NewScanner(r)
	for s.Scan() {
		ln := strings.TrimSpace(s.Text())
		if strings.HasPrefix(ln, "#") || ln == "" {
			continue
		}
		cidx := strings.Index(ln, "#")
		if cidx > 0 {
			ln = ln[:cidx]
		}
		ln = strings.TrimSpace(ln)
		parts := strings.Fields(ln)
		if len(parts) != 2 && len(parts) != 3 {
			return nil, fmt.Errorf("invalid config format: %s", ln)
		}
		deps = append(deps, dependency{
			Name:   parts[0],
			Commit: parts[1],
		})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}

func getPreviousDeps(previous string) ([]dependency, error) {
	r, err := fileFromRev(previous, vendorConf)
	if err != nil {
		return nil, err
	}
	return parseDependencies(r)
}

func changelog(previous, commit string) ([]change, error) {
	raw, err := getChangelog(previous, commit)
	if err != nil {
		return nil, err
	}
	return parseChangelog(raw)
}

func getChangelog(previous, commit string) ([]byte, error) {
	return git("log", "--oneline", fmt.Sprintf("%s..%s", previous, commit))
}

func parseChangelog(changelog []byte) ([]change, error) {
	var (
		changes []change
		s       = bufio.NewScanner(bytes.NewReader(changelog))
	)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		changes = append(changes, change{
			Commit:      fields[0],
			Description: strings.Join(fields[1:], " "),
		})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return changes, nil
}

func fileFromRev(rev, file string) (io.Reader, error) {
	p, err := git("show", fmt.Sprintf("%s:%s", rev, file))
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(p), nil
}

func git(args ...string) ([]byte, error) {
	o, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, o)
	}
	return o, nil
}

func updatedDeps(previous, deps []dependency) []dependency {
	var updated []dependency
	pm, cm := toDepMap(previous), toDepMap(deps)
	for name, c := range cm {
		d, ok := pm[name]
		if !ok {
			// it is a new dep and should be noted
			updated = append(updated, c)
			continue
		}
		// it exists, see if its updated
		if d.Commit != c.Commit {
			// set the previous commit
			c.Previous = d.Commit
			updated = append(updated, c)
		}
	}
	return updated
}

func toDepMap(deps []dependency) map[string]dependency {
	out := make(map[string]dependency)
	for _, d := range deps {
		out[d.Name] = d
	}
	return out
}

func getContributors(previous, commit string) ([]string, error) {
	raw, err := git("log", "--format=%aN", fmt.Sprintf("%s..%s", previous, commit))
	if err != nil {
		return nil, err
	}
	var (
		set = make(map[string]struct{})
		s   = bufio.NewScanner(bytes.NewReader(raw))
		out []string
	)
	for s.Scan() {
		set[s.Text()] = struct{}{}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
