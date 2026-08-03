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

package internal

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/containerd/log"
	"github.com/containerd/ttrpc"
)

func Serve(ctx context.Context, l net.Listener, serveFunc func(net.Listener) error) {
	path := l.Addr().String()
	log.G(ctx).WithField("address", path).Info("serving...")
	go func() {
		defer l.Close()

		if err := serveFunc(l); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, ttrpc.ErrServerClosed) {
			log.G(ctx).WithError(err).WithField("address", path).Fatal("serve failure")
		}
	}()
}
