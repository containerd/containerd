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

package sys

import (
	"fmt"
	"net"
	osuser "os/user"

	"github.com/containerd/errdefs"

	"github.com/Microsoft/go-winio"
)

// GetLocalListener returns a Listernet out of a named pipe.
// `path` must be of the form of `\\.\pipe\<pipename>`
// (see https://msdn.microsoft.com/en-us/library/windows/desktop/aa365150)
func GetLocalListener(path string, uid, gid int, user, group string) (net.Listener, error) {
	if uid != 0 {
		return nil, fmt.Errorf("cannot specify numeric uid: %w", errdefs.ErrInvalidArgument)
	}

	if gid != 0 {
		return nil, fmt.Errorf("cannot specify numeric gid: %w", errdefs.ErrInvalidArgument)
	}

	pc := winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;BA)(A;;GA;;;SY)"}

	if user != "" {
		userinfo, err := osuser.Lookup(user)
		if err != nil {
			return nil, err
		}

		pc.SecurityDescriptor += fmt.Sprintf("(A;;GRGW;;;%s)", userinfo.Uid)
	}

	if group != "" {
		groupinfo, err := osuser.LookupGroup(group)
		if err != nil {
			return nil, err
		}

		pc.SecurityDescriptor += fmt.Sprintf("(A;;GRGW;;;%s)", groupinfo.Gid)
	}

	return winio.ListenPipe(path, &pc)
}
