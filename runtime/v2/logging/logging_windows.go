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
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Microsoft/go-winio"
)

// Run the logging driver
func Run(fn LoggerFunc) {
	err := runInternal(fn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runInternal(fn LoggerFunc) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		soutPipe, serrPipe, waitPipe string
		sout, serr, wait             net.Conn
		ok                           bool
		err                          error
	)

	if soutPipe, ok = os.LookupEnv("CONTAINER_STDOUT"); !ok {
		return errors.New("'CONTAINER_STDOUT' environment variable missing")
	}
	if sout, err = winio.DialPipeContext(ctx, soutPipe); err != nil {
		return fmt.Errorf("unable to dial stdout pipe: %w", err)
	}

	if serrPipe, ok = os.LookupEnv("CONTAINER_STDERR"); !ok {
		return errors.New("'CONTAINER_STDERR' environment variable missing")
	}
	if serr, err = winio.DialPipeContext(ctx, serrPipe); err != nil {
		return fmt.Errorf("unable to dial stderr pipe: %w", err)
	}

	waitPipe = os.Getenv("CONTAINER_WAIT")
	if wait, err = winio.DialPipeContext(ctx, waitPipe); err != nil {
		return fmt.Errorf("unable to dial wait pipe: %w", err)
	}

	config := &Config{
		ID:        os.Getenv("CONTAINER_ID"),
		Namespace: os.Getenv("CONTAINER_NAMESPACE"),
		Stdout:    sout,
		Stderr:    serr,
	}

	var (
		sigCh = make(chan os.Signal, 2)
		errCh = make(chan error, 1)
	)

	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		errCh <- fn(ctx, config, wait.Close)
	}()

	for {
		select {
		case <-sigCh:
			cancel()
		case err = <-errCh:
			return err
		}
	}
}
