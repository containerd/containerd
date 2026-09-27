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
	"context"
	"fmt"
	"os"

	"github.com/containerd/containerd/v2/cmd/containerd/command"

	_ "github.com/containerd/containerd/v2/cmd/containerd/builtins"
)

func main() {
	ctx := context.Background()
	if err := command.App().Run(ctx, os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "containerd:", err)
		os.Exit(1)
	}
}
