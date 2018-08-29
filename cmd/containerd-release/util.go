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
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

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

		var cloneURL string
		if len(parts) == 3 {
			cloneURL = parts[2]
		} else {
			cloneURL = "git://" + parts[0]
		}

		deps = append(deps, dependency{
			Name:     parts[0],
			Commit:   parts[1],
			CloneURL: cloneURL,
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

func gitChangeDiff(previous, commit string) string {
	if previous != "" {
		return fmt.Sprintf("%s..%s", previous, commit)
	}
	return commit
}

func getChangelog(previous, commit string) ([]byte, error) {
	return git("log", "--oneline", gitChangeDiff(previous, commit))
}

func linkifyChanges(c []change, commit, msg func(change) (string, error)) error {
	for i := range c {
		commitLink, err := commit(c[i])
		if err != nil {
			return err
		}

		description, err := msg(c[i])
		if err != nil {
			return err
		}

		c[i].Commit = fmt.Sprintf("[`%s`](%s)", c[i].Commit, commitLink)
		c[i].Description = description

	}

	return nil
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

var gitConfigs = map[string]string{}

func git(args ...string) ([]byte, error) {
	var gitArgs []string
	for k, v := range gitConfigs {
		gitArgs = append(gitArgs, "-c", fmt.Sprintf("%s=%s", k, v))
	}
	gitArgs = append(gitArgs, args...)
	o, err := exec.Command("git", gitArgs...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %s", err, o)
	}
	return o, nil
}

func renameDependencies(deps []dependency, renames map[string]projectRename) {
	if len(renames) == 0 {
		return
	}
	type dep struct {
		shortname string
		name      string
	}
	renameMap := map[string]dep{}
	for shortname, rename := range renames {
		renameMap[rename.Old] = dep{
			shortname: shortname,
			name:      rename.New,
		}
	}
	for i := range deps {
		if updated, ok := renameMap[deps[i].Name]; ok {
			logrus.Debugf("Renamed %s from %s to %s", updated.shortname, deps[i].Name, updated.name)
			deps[i].Name = updated.name
		}
	}
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

type contributor struct {
	name  string
	email string
}

func addContributors(previous, commit string, contributors map[contributor]int) error {
	raw, err := git("log", `--format=%aE %aN`, gitChangeDiff(previous, commit))
	if err != nil {
		return err
	}
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		p := strings.SplitN(s.Text(), " ", 2)
		if len(p) != 2 {
			return errors.Errorf("invalid author line: %q", s.Text())
		}
		c := contributor{
			name:  p[1],
			email: p[0],
		}
		contributors[c] = contributors[c] + 1
	}
	if err := s.Err(); err != nil {
		return err
	}
	return nil
}

func orderContributors(contributors map[contributor]int) []string {
	type contribstat struct {
		name  string
		email string
		count int
	}
	all := make([]contribstat, 0, len(contributors))
	for c, count := range contributors {
		all = append(all, contribstat{
			name:  c.name,
			email: c.email,
			count: count,
		})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count == all[j].count {
			return all[i].name < all[j].name
		}
		return all[i].count > all[j].count
	})
	names := make([]string, len(all))
	for i := range names {
		logrus.Debugf("Contributor: %s <%s> with %d commits", all[i].name, all[i].email, all[i].count)
		names[i] = all[i].name
	}

	return names
}

// getTemplate will use a builtin template if the template is not specified on the cli
func getTemplate(context *cli.Context) (string, error) {
	path := context.GlobalString("template")
	f, err := os.Open(path)
	if err != nil {
		// if the template file does not exist and the path is for the default template then
		// return the compiled in template
		if os.IsNotExist(err) && path == defaultTemplateFile {
			return releaseNotes, nil
		}
		return "", err
	}
	defer f.Close()
	data, err := ioutil.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func githubCommitLink(repo string) func(change) (string, error) {
	return func(c change) (string, error) {
		full, err := git("rev-parse", c.Commit)
		if err != nil {
			return "", err
		}
		commit := strings.TrimSpace(string(full))

		return fmt.Sprintf("https://github.com/%s/commit/%s", repo, commit), nil
	}
}

func githubPRLink(repo string) func(change) (string, error) {
	r := regexp.MustCompile("^Merge pull request #[0-9]+")
	return func(c change) (string, error) {
		var err error
		message := r.ReplaceAllStringFunc(c.Description, func(m string) string {
			idx := strings.Index(m, "#")
			pr := m[idx+1:]

			// TODO: Validate links using github API
			// TODO: Validate PR merged as commit hash
			link := fmt.Sprintf("https://github.com/%s/pull/%s", repo, pr)

			return fmt.Sprintf("%s [#%s](%s)", m[:idx], pr, link)
		})
		if err != nil {
			return "", err
		}
		return message, nil
	}
}
