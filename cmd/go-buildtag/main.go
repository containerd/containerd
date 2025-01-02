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
	"io"
	"os"
)

func main() {
	wrFl := flag.Bool("w", false, "Write to file")
	tagsFl := flag.String("tags", "", "Tags to add")

	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: go-buildtag <options> <file1> [<file2> <file3> ...]")
		os.Exit(1)
	}

	wr := *wrFl
	tags := *tagsFl
	for i := 0; i < flag.NArg(); i++ {
		p := flag.Arg(i)
		if err := handle(wr, tags, p); err != nil {
			fmt.Fprintln(os.Stderr, p+":", err)
			os.Exit(2)
		}
	}
}

func handle(wr bool, tags, p string) (retErr error) {
	out := os.Stdout

	rdr, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("could not open source file: %w", err)
	}
	defer rdr.Close()

	if wr {
		f, err := os.Create(p + ".tmp")
		if err != nil {
			return fmt.Errorf("error creatign temp file")
		}
		defer func() {
			if retErr != nil {
				os.Remove(f.Name())
			}
		}()

		defer f.Close()
		out = f
	}

	_, err = fmt.Fprintf(out, "//go:build %s\n\n", tags)
	if err != nil {
		return fmt.Errorf("error writing build tags: %w", err)
	}

	_, err = io.Copy(out, rdr)
	if err != nil {
		return fmt.Errorf("error copying original content to temp file")
	}

	rdr.Close()
	if wr {
		out.Close()

		if err := os.Remove(p); err != nil {
			return fmt.Errorf("error removing original source file: %w", err)
		}

		if err := os.Rename(p+".tmp", p); err != nil {
			return fmt.Errorf("error replacing original source: %w", err)
		}
	}

	return nil
}
