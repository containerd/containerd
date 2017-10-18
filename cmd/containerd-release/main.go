package main

import (
	"fmt"
	"os"
	"text/template"

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
}

type download struct {
	Filename string
	Hash     string
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
	Downloads    []download
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
		logrus.Info("release complete!")
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
}
