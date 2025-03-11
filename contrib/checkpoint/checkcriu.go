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

// checkcriu is a tool to check if CRIU is actually available and
// at least criu.PodCriuVersion (3.16). This is the first version
// of CRIU which can handle checkpointing and restoring containers
// out of pods and into pods (shared pid namespace).

package main

import (
	criu "github.com/checkpoint-restore/go-criu/v7/utils"
)

func main() {
	if err := criu.CheckForCriu(criu.PodCriuVersion); err != nil {
		panic(err)
	}
}
