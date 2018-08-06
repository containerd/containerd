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

package v2

import (
	"context"
	"fmt"
	"io"

	winio "github.com/Microsoft/go-winio"
	"github.com/containerd/containerd/namespaces"
)

func openShimLog(ctx context.Context, bundle *Bundle) (io.ReadCloser, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	l, err := winio.ListenPipe(fmt.Sprintf("\\\\.\\pipe\\containerd-shim-%s-%s-log", ns, bundle.ID), nil)
	if err != nil {
		return nil, err
	}
	c, err := l.Accept()
	if err != nil {
		l.Close()
	}
	return c, nil
}
