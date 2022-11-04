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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/cmd/containerd/command"
	"github.com/containerd/containerd/cmd/ctr/app"
	"github.com/urfave/cli"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()
	apps := map[string]*cli.App{
		"containerd": command.App(),
		"ctr":        app.New(),
	}
	dir := flag.Arg(1)

	name, section, ok := strings.Cut(flag.Arg(0), ".")
	if !ok {
		return fmt.Errorf("invalid name '%s': name does not contain man page section", flag.Arg(0))
	}

	appName, ok := apps[name]
	if !ok {
		return fmt.Errorf("invalid application '%s'", name)
	}

	// clear out the usage as we use banners that do not display in man pages
	appName.Usage = ""
	data, err := appName.ToMan()
	if err != nil {
		return err
	}
	_ = os.MkdirAll(dir, os.ModePerm)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.%s", name, section)), []byte(data), 0644); err != nil {
		return err
	}
	return nil
}
