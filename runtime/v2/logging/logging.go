// +build !windows

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
	"io"
	"os"
	"os/signal"

	"golang.org/x/sys/unix"
)

// Config of the container logs
type Config struct {
	ID        string
	Namespace string
	Stdout    io.Reader
	Stderr    io.Reader
}

// LoggerFunc is implemented by custom v2 logging binaries
type LoggerFunc func(context.Context, *Config, func() error) error

// Run the logging driver
func Run(fn LoggerFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	config := &Config{
		ID:        os.Getenv("CONTAINER_ID"),
		Namespace: os.Getenv("CONTAINER_NAMESPACE"),
		Stdout:    os.NewFile(3, "CONTAINER_STDOUT"),
		Stderr:    os.NewFile(4, "CONTAINER_STDERR"),
	}
	var (
		s     = make(chan os.Signal, 32)
		errCh = make(chan error, 1)
		wait  = os.NewFile(5, "CONTAINER_WAIT")
	)
	signal.Notify(s, unix.SIGTERM)

	go func() {
		if err := fn(ctx, config, wait.Close); err != nil {
			errCh <- err
		}
		errCh <- nil
	}()

	for {
		select {
		case <-s:
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
