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
	"os/exec"
)

func main() {
	// Launch a slow child process by re-executing this binary with the -sleep-forever flag.
	if len(os.Args) == 2 && os.Args[1] == "-sleep-forever" {
		fmt.Println("sleeping forever...")
		for {
		}
	}

	thisBin := os.Args[0]
	cmd := exec.Command(thisBin, "-sleep-forever")
	b, err := cmd.CombinedOutput()
	fmt.Println(string(b))
	if err != nil {
		panic(err)
	}
}
