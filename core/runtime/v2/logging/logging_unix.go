//go:build !windows

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
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

// Run the logging driver
func Run(fn LoggerFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := &Config{
		ID:        os.Getenv("CONTAINER_ID"),
		Namespace: os.Getenv("CONTAINER_NAMESPACE"),
		Stdout:    os.NewFile(3, "CONTAINER_STDOUT"),
		Stderr:    os.NewFile(4, "CONTAINER_STDERR"),
	}
	var (
		sigCh = make(chan os.Signal, 32)
		errCh = make(chan error, 1)
		wait  = os.NewFile(5, "CONTAINER_WAIT")
	)
	signal.Notify(sigCh, unix.SIGTERM)

	go func() {
		errCh <- fn(ctx, config, wait.Close)
	}()

	for {
		select {
		case <-sigCh:
			cancel()
		case err := <-errCh:
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
}
