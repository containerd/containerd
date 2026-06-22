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
)

func main() {
	n := 500000
	fmt.Fprintf(os.Stderr, "attempting to write %v bytes to stdout\n", n)

	// Writing this all in one fmt.Print has a different interaction with stdout
	// pipe buffering than writing one byte at a time.
	wrote := 0
	for i := 0; i < n; i++ {
		w, err := fmt.Print("A")
		if err != nil {
			fmt.Fprintf(os.Stderr, "got error writing to stdout: %v\n", err)
		}

		wrote += w

		if i%10000 == 0 {
			fmt.Fprintf(os.Stderr, "progress: wrote %v bytes to stdout\n", wrote)
		}
	}

	fmt.Fprintf(os.Stderr, "wrote %v bytes to stdout\n", wrote)
}
