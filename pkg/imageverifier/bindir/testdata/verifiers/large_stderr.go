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
	"os"
	"strings"
)

func main() {
	n := 50000
	fmt.Fprintf(os.Stderr, "attempting to write %v bytes to stderr\n", n)

	wrote, err := fmt.Fprintf(os.Stderr, strings.Repeat("A", n))
	if err != nil {
		fmt.Fprintf(os.Stderr, "got error writing to stderr: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "wrote %v bytes to stderr\n", wrote)
}
