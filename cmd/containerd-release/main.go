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
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"text/template"
	"unicode"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

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
	CloneURL string
}

type download struct {
	Filename string
	Hash     string
}

type projectChange struct {
	Name    string
	Changes []change
}

type projectRename struct {
	Old string `toml:"old"`
	New string `toml:"new"`
}

type release struct {
	ProjectName     string            `toml:"project_name"`
	GithubRepo      string            `toml:"github_repo"`
	Commit          string            `toml:"commit"`
	Previous        string            `toml:"previous"`
	PreRelease      bool              `toml:"pre_release"`
	Preface         string            `toml:"preface"`
	Notes           map[string]note   `toml:"notes"`
	BreakingChanges map[string]change `toml:"breaking"`

	// dependency options
	MatchDeps  string                   `toml:"match_deps"`
	RenameDeps map[string]projectRename `toml:"rename_deps"`

	// generated fields
	Changes      []projectChange
	Contributors []string
	Dependencies []dependency
	Tag          string
	Version      string
	Downloads    []download
}

func main() {
	app := cli.NewApp()
	app.Name = "release"
	app.Description = `release tooling.

This tool should be ran from the root of the project repository for a new release.
`
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "dry,n",
			Usage: "run the release tooling as a dry run to print the release notes to stdout",
		},
		cli.BoolFlag{
			Name:  "debug,d",
			Usage: "show debug output",
		},
		cli.StringFlag{
			Name:  "tag,t",
			Usage: "tag name for the release, defaults to release file name",
		},
		cli.StringFlag{
			Name:  "template",
			Usage: "template filepath to use in place of the default",
			Value: defaultTemplateFile,
		},
		cli.BoolFlag{
			Name:  "linkify,l",
			Usage: "add links to changelog",
		},
	}
	app.Action = func(context *cli.Context) error {
		var (
			releasePath = context.Args().First()
			tag         = context.String("tag")
			linkify     = context.Bool("linkify")
		)
		if tag == "" {
			tag = parseTag(releasePath)
		}
		version := strings.TrimLeft(tag, "v")
		if context.Bool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		r, err := loadRelease(releasePath)
		if err != nil {
			return err
		}
		logrus.Infof("Welcome to the %s release tool...", r.ProjectName)

		mailmapPath, err := filepath.Abs(".mailmap")
		if err != nil {
			return errors.Wrap(err, "failed to resolve mailmap")
		}
		gitConfigs["mailmap.file"] = mailmapPath

		var (
			contributors   = map[contributor]int{}
			projectChanges = []projectChange{}
		)

		changes, err := changelog(r.Previous, r.Commit)
		if err != nil {
			return err
		}
		if linkify {
			if err := linkifyChanges(changes, githubCommitLink(r.GithubRepo), githubPRLink(r.GithubRepo)); err != nil {
				return err
			}
		}
		if err := addContributors(r.Previous, r.Commit, contributors); err != nil {
			return err
		}
		projectChanges = append(projectChanges, projectChange{
			Name:    "",
			Changes: changes,
		})

		logrus.Infof("creating new release %s with %d new changes...", tag, len(changes))
		rd, err := fileFromRev(r.Commit, vendorConf)
		if err != nil {
			return err
		}
		previous, err := getPreviousDeps(r.Previous)
		if err != nil {
			return err
		}
		deps, err := parseDependencies(rd)
		if err != nil {
			return err
		}
		renameDependencies(previous, r.RenameDeps)
		updatedDeps := updatedDeps(previous, deps)

		sort.Slice(updatedDeps, func(i, j int) bool {
			return updatedDeps[i].Name < updatedDeps[j].Name
		})

		if r.MatchDeps != "" && len(updatedDeps) > 0 {
			re, err := regexp.Compile(r.MatchDeps)
			if err != nil {
				return errors.Wrap(err, "unable to compile 'match_deps' regexp")
			}
			td, err := ioutil.TempDir("", "tmp-clone-")
			if err != nil {
				return errors.Wrap(err, "unable to create temp clone directory")
			}
			defer os.RemoveAll(td)

			cwd, err := os.Getwd()
			if err != nil {
				return errors.Wrap(err, "unable to get cwd")
			}
			for _, dep := range updatedDeps {
				matches := re.FindStringSubmatch(dep.Name)
				if matches == nil {
					continue
				}
				logrus.Debugf("Matched dependency %s with %s", dep.Name, r.MatchDeps)
				var name string
				if len(matches) < 2 {
					name = path.Base(dep.Name)
				} else {
					name = matches[1]
				}
				if err := os.Chdir(td); err != nil {
					return errors.Wrap(err, "unable to chdir to temp clone directory")
				}
				git("clone", dep.CloneURL, name)

				if err := os.Chdir(name); err != nil {
					return errors.Wrapf(err, "unable to chdir to cloned %s directory", name)
				}

				changes, err := changelog(dep.Previous, dep.Commit)
				if err != nil {
					return errors.Wrapf(err, "failed to get changelog for %s", name)
				}
				if err := addContributors(dep.Previous, dep.Commit, contributors); err != nil {
					return errors.Wrapf(err, "failed to get authors for %s", name)
				}
				if linkify {
					if !strings.HasPrefix(dep.Name, "github.com/") {
						logrus.Debugf("linkify only supported for Github, skipping %s", dep.Name)
					} else {
						ghname := dep.Name[11:]
						if err := linkifyChanges(changes, githubCommitLink(ghname), githubPRLink(ghname)); err != nil {
							return err
						}
					}
				}

				projectChanges = append(projectChanges, projectChange{
					Name:    name,
					Changes: changes,
				})

			}
			if err := os.Chdir(cwd); err != nil {
				return errors.Wrap(err, "unable to chdir to previous cwd")
			}
		}

		// update the release fields with generated data
		r.Contributors = orderContributors(contributors)
		r.Dependencies = updatedDeps
		r.Changes = projectChanges
		r.Tag = tag
		r.Version = version

		// Remove trailing new lines
		r.Preface = strings.TrimRightFunc(r.Preface, unicode.IsSpace)

		tmpl, err := getTemplate(context)
		if err != nil {
			return err
		}

		if context.Bool("dry") {
			t, err := template.New("release-notes").Parse(tmpl)
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 8, 8, 2, ' ', 0)
			if err := t.Execute(w, r); err != nil {
				return err
			}
			return w.Flush()
		}
		logrus.Info("release complete!")
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
