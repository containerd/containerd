//go:build gofuzz

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

package sbserver

import (
	"fmt"

	"github.com/containerd/containerd/pkg/cri/server"
	"github.com/containerd/containerd/pkg/cri/store/sandbox"
)

func SandboxStore(cs server.CRIService) (*sandbox.Store, error) {
	s, ok := cs.(*criService)
	if !ok {
		return nil, fmt.Errorf("%+v is not sbserver.criService", cs)
	}
	return s.sandboxStore, nil
}
