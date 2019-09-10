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

package climan

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/urfave/cli"
)

var Command = cli.Command{
	Name:   "gen-man",
	Usage:  "generate man pages for the cli application",
	Hidden: true,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "format,f",
			Usage: "specify the format in (md:man)",
			Value: "md",
		},
		cli.IntFlag{
			Name:  "section,s",
			Usage: "section of the man pages",
			Value: 1,
		},
	},
	Action: func(clix *cli.Context) (err error) {
		// clear out the usage as we use banners that do not display in man pages
		clix.App.Usage = ""
		dir := clix.Args().First()
		if dir == "" {
			return errors.New("directory argument is required")
		}
		var (
			data string
			ext  string
		)
		switch clix.String("format") {
		case "man":
			data, err = clix.App.ToMan()
		default:
			data, err = clix.App.ToMarkdown()
			ext = "md"
		}
		if err != nil {
			return err
		}
		return ioutil.WriteFile(filepath.Join(dir, formatFilename(clix, clix.Int("section"), ext)), []byte(data), 0644)
	},
}

func formatFilename(clix *cli.Context, section int, ext string) string {
	s := fmt.Sprintf("%s.%d", clix.App.Name, section)
	if ext != "" {
		s += "." + ext
	}
	return s
}
