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
	"fmt"
	"net"
	"syscall"
	"time"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/namespaces"
	"github.com/pkg/errors"
)

func getSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// SetScore sets the oom score for a process
func SetScore(pid int) error {
	return nil
}

// SocketAddress returns a npipe address
func SocketAddress(ctx context.Context, id string) (string, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("\\\\.\\pipe\\containerd-shim-%s-%s-pipe", ns, id), nil
}

// AnonDialer returns a dialer for a npipe
func AnonDialer(address string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(address, &timeout)
}

// NewSocket returns a new npipe listener
func NewSocket(address string) (net.Listener, error) {
	l, err := winio.ListenPipe(address, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to listen to npipe %s", address)
	}
	return l, nil
}
