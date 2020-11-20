// +build windows

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

package logging

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
)

// Run the logging driver
func Run(fn LoggerFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var sout, serr net.Conn

	if soutPipe := os.Getenv("CONTAINER_STDOUT"); soutPipe != "" {
		locOut, err := winio.DialPipeContext(ctx, soutPipe)

		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sout = locOut
	}

	if serrPipe := os.Getenv("CONTAINER_STDERR"); serrPipe != "" {
		locSerr, err := winio.DialPipeContext(ctx, serrPipe)

		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		serr = locSerr
	}

	waitPipe := os.Getenv("CONTAINER_WAIT")
	wait, err := winio.DialPipeContext(ctx, waitPipe)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	config := &Config{
		ID:        os.Getenv("CONTAINER_ID"),
		Namespace: os.Getenv("CONTAINER_NAMESPACE"),
		Stdout:    sout,
		Stderr:    serr,
	}

	var errCh = make(chan error)

	go func() {
		errCh <- fn(ctx, config, wait.Close)
	}()

	if err := <-errCh; err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
