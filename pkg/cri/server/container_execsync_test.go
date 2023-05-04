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

package server

import (
	"bytes"
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/v2"
	"github.com/containerd/containerd/v2/cio"
	cioutil "github.com/containerd/containerd/v2/pkg/ioutil"
	"github.com/stretchr/testify/assert"
)

func TestCWWrite(t *testing.T) {
	var buf bytes.Buffer
	cw := &cappedWriter{w: cioutil.NewNopWriteCloser(&buf), remain: 10}

	n, err := cw.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)

	n, err = cw.Write([]byte("helloworld"))
	assert.NoError(t, err, "no errors even it hits the cap")
	assert.Equal(t, 10, n, "no indication of partial write")
	assert.True(t, cw.isFull())
	assert.Equal(t, []byte("hellohello"), buf.Bytes(), "the underlying writer is capped")

	_, err = cw.Write([]byte("world"))
	assert.NoError(t, err)
	assert.True(t, cw.isFull())
	assert.Equal(t, []byte("hellohello"), buf.Bytes(), "the underlying writer is capped")
}

func TestCWClose(t *testing.T) {
	var buf bytes.Buffer
	cw := &cappedWriter{w: cioutil.NewNopWriteCloser(&buf), remain: 5}
	err := cw.Close()
	assert.NoError(t, err)
}

func TestDrainExecSyncIO(t *testing.T) {
	ctx := context.TODO()

	t.Run("NoTimeout", func(t *testing.T) {
		ep := &fakeExecProcess{
			id:  t.Name(),
			pid: uint32(os.Getpid()),
		}

		attachDoneCh := make(chan struct{})
		time.AfterFunc(2*time.Second, func() { close(attachDoneCh) })
		assert.NoError(t, drainExecSyncIO(ctx, ep, 0, attachDoneCh))
		assert.Equal(t, 0, len(ep.actionEvents))
	})

	t.Run("With3Seconds", func(t *testing.T) {
		ep := &fakeExecProcess{
			id:  t.Name(),
			pid: uint32(os.Getpid()),
		}

		attachDoneCh := make(chan struct{})
		time.AfterFunc(100*time.Second, func() { close(attachDoneCh) })
		assert.Error(t, drainExecSyncIO(ctx, ep, 3*time.Second, attachDoneCh))
		assert.Equal(t, []string{"Delete"}, ep.actionEvents)
	})
}

type fakeExecProcess struct {
	id           string
	pid          uint32
	actionEvents []string
}

// ID of the process
func (p *fakeExecProcess) ID() string {
	return p.id
}

// Pid is the system specific process id
func (p *fakeExecProcess) Pid() uint32 {
	return p.pid
}

// Start starts the process executing the user's defined binary
func (p *fakeExecProcess) Start(context.Context) error {
	p.actionEvents = append(p.actionEvents, "Start")
	return nil
}

// Delete removes the process and any resources allocated returning the exit status
func (p *fakeExecProcess) Delete(context.Context, ...containerd.ProcessDeleteOpts) (*containerd.ExitStatus, error) {
	p.actionEvents = append(p.actionEvents, "Delete")
	return nil, nil
}

// Kill sends the provided signal to the process
func (p *fakeExecProcess) Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error {
	p.actionEvents = append(p.actionEvents, "Kill")
	return nil
}

// Wait asynchronously waits for the process to exit, and sends the exit code to the returned channel
func (p *fakeExecProcess) Wait(context.Context) (<-chan containerd.ExitStatus, error) {
	p.actionEvents = append(p.actionEvents, "Wait")
	return nil, nil
}

// CloseIO allows various pipes to be closed on the process
func (p *fakeExecProcess) CloseIO(context.Context, ...containerd.IOCloserOpts) error {
	p.actionEvents = append(p.actionEvents, "CloseIO")
	return nil
}

// Resize changes the width and height of the process's terminal
func (p *fakeExecProcess) Resize(ctx context.Context, w, h uint32) error {
	p.actionEvents = append(p.actionEvents, "Resize")
	return nil
}

// IO returns the io set for the process
func (p *fakeExecProcess) IO() cio.IO {
	p.actionEvents = append(p.actionEvents, "IO")
	return nil
}

// Status returns the executing status of the process
func (p *fakeExecProcess) Status(context.Context) (containerd.Status, error) {
	p.actionEvents = append(p.actionEvents, "Status")
	return containerd.Status{}, nil
}
