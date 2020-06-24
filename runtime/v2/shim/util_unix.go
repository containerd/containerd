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

package shim

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/dialer"
	"github.com/containerd/containerd/sys"
	"github.com/pkg/errors"
)

const (
	shimBinaryFormat = "containerd-shim-%s-%s"
	socketPathLimit  = 106
)

func getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// SetScore sets the oom score for a process
func SetScore(pid int) error {
	return sys.SetOOMScore(pid, sys.OOMScoreMaxKillable)
}

// AdjustOOMScore sets the OOM score for the process to the parents OOM score +1
// to ensure that they parent has a lower* score than the shim
func AdjustOOMScore(pid int) error {
	parent := os.Getppid()
	score, err := sys.GetOOMScoreAdj(parent)
	if err != nil {
		return errors.Wrap(err, "get parent OOM score")
	}
	shimScore := score + 1
	if err := sys.SetOOMScore(pid, shimScore); err != nil {
		return errors.Wrap(err, "set shim OOM score")
	}
	return nil
}

// SocketAddress returns a socket address
func SocketAddress(ctx context.Context, id string) (string, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll("/run/containerd/s", 0660); err != nil {
		return "", err
	}
	d := sha256.Sum256([]byte(filepath.Join(ns, id)))
	return fmt.Sprintf("unix:///run/containerd/s/%x", d), nil
}

// AnonDialer returns a dialer for a socket
func AnonDialer(address string, timeout time.Duration) (net.Conn, error) {
	return dialer.Dialer(socket(address).path(), timeout)
}

func AnonReconnectDialer(address string, timeout time.Duration) (net.Conn, error) {
	return AnonDialer(address, timeout)
}

// NewSocket returns a new socket
func NewSocket(address string) (*net.UnixListener, error) {
	var (
		sock = socket(address)
		path = sock.path()
	)
	if !sock.isAbstract() {
		if err := os.MkdirAll(filepath.Dir(path), 0660); err != nil {
			return nil, errors.Wrapf(err, "%s", path)
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return l.(*net.UnixListener), nil
}

// RemoveSocket removes the socket at the sepcified address if
// it exists on the filesystem
func RemoveSocket(address string) error {
	sock := socket(address)
	if !sock.isAbstract() {
		return os.Remove(sock.path())
	}
	return nil
}
