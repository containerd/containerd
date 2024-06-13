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
	"crypto"
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/cmd/ctr/app"
	"github.com/containerd/containerd/v2/internal/hasher"
	"github.com/urfave/cli/v2"
)

var pluginCmds = []*cli.Command{}

func init() {
	crypto.RegisterHash(crypto.SHA256, hasher.NewSHA256)
}

func main() {
	app := app.New()
	app.Commands = append(app.Commands, pluginCmds...)
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "ctr: %s\n", err)
		os.Exit(1)
	}
}
