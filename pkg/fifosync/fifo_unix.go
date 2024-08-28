//go:build unix

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

/*
Package fifosync provides a pattern on Unix-like operating systems for synchronizing across processes using Unix FIFOs
(named pipes).
*/
package fifosync

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Trigger is a FIFO which is used to signal another process to proceed.
type Trigger interface {
	// Name returns the name of the trigger
	Name() string
	// Trigger triggers another process to proceed.
	Trigger() error
}

// Waiter is a FIFO which is used to wait for trigger provided by another process.
type Waiter interface {
	// Name returns the name of the waiter
	Name() string
	// Wait waits for a trigger from another process.
	Wait() error
}

type fifo struct {
	name string
}

// NewTrigger creates a new Trigger
func NewTrigger(name string, mode uint32) (Trigger, error) {
	return new(name, mode)
}

// NewWaiter creates a new Waiter
func NewWaiter(name string, mode uint32) (Waiter, error) {
	return new(name, mode)
}

// New creates a new FIFO if it does not already exist. Use AsTrigger or AsWaiter to convert the new FIFO to a Trigger
// or Waiter.
func new(name string, mode uint32) (*fifo, error) {
	s, err := os.Stat(name)
	exist := true
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("fifo: failed to stat %q: %w", name, err)
		}
		exist = false
	}
	if s != nil && s.Mode()&os.ModeNamedPipe == 0 {
		return nil, fmt.Errorf("fifo: not a named pipe: %q", name)
	}
	if !exist {
		err = unix.Mkfifo(name, mode)
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("fifo: failed to create %q: %w", name, err)
		}
	}
	return &fifo{
		name: name,
	}, nil
}

func (f *fifo) Name() string {
	return f.name
}

// AsTrigger converts the FIFO to a Trigger.
func (f *fifo) AsTrigger() Trigger {
	return f
}

// Trigger triggers another process to proceed.
func (f *fifo) Trigger() error {
	file, err := os.OpenFile(f.name, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("fifo: failed to open %s: %w", f.name, err)
	}
	defer file.Close()
	_, err = io.ReadAll(file)
	return err
}

// AsWaiter converts the FIFO to a Waiter.
func (f *fifo) AsWaiter() Waiter {
	return f
}

// Wait waits for a trigger from another process.
func (f *fifo) Wait() error {
	fd, err := unix.Open(f.name, unix.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("fifo: failed to open %s: %w", f.name, err)
	}
	defer unix.Close(fd)
	if _, err := unix.Write(fd, []byte("0")); err != nil {
		return fmt.Errorf("failed to write to %d: %w", fd, err)
	}
	return nil
}
