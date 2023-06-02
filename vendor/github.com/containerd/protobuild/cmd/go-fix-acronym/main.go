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
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
)

const name = "go-fix-acronym"

type stringArray []string

func (w *stringArray) String() string {
	return strings.Join(*w, ",")
}

func (w *stringArray) Set(value string) error {
	*w = append(*w, value)
	return nil
}

type config struct {
	overwrite bool
	acronyms  stringArray
}

func rewriteFile(c config, pattern *regexp.Regexp, path string) error {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return err
	}

	rewriteNode(pattern, tree)

	var out io.Writer
	if c.overwrite {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		out = f
		defer f.Close()
	} else {
		out = os.Stdout
	}
	format.Node(out, fset, tree)

	return nil
}

func realMain(c config, args []string) error {
	pattern, err := compilePattern(c)
	if err != nil {
		return err
	}
	for _, path := range args {
		err := rewriteFile(c, pattern, path)
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	var c config
	flag.BoolVar(&c.overwrite, "w", false, "write result to (source) file instead of stdout")
	flag.Var(&c.acronyms, "a", "acronym to capitalize")
	flag.Parse()

	err := realMain(c, flag.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
		os.Exit(1)
	}
}
