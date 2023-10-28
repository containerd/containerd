/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you		p :=
 may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package platform

import (
	"fmt"
	"github.com/containerd/containerd/platforms"
	"github.com/urfave/cli"
	"os"
	"text/tabwriter"
)

// Command is a cli command that outputs current platform
var Command = cli.Command{
	Name:  "platform",
	Usage: "Provides information about current local platform",
	Action: func(context *cli.Context) error {
		w := tabwriter.NewWriter(os.Stdout, 4, 8, 4, ' ', 0)
		fmt.Fprintf(w, "%s\n", platforms.Format(platforms.DefaultSpec()))
		return w.Flush()
	},
}
