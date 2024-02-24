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

package credential

import (
	"context"
	"fmt"
	"sync"

	transfertypes "github.com/containerd/containerd/v2/api/types/transfer"
)

type memoryStore struct {
	creds map[string]*Credentials
	sync.RWMutex
}

func newMemoryStore() Manager {
	return &memoryStore{
		creds: make(map[string]*Credentials),
	}
}

func (m *memoryStore) Store(ctx context.Context, creds Credentials) error {
	m.Lock()
	defer m.Unlock()
	m.creds[creds.Host] = &creds
	return nil
}

func (m *memoryStore) Delete(ctx context.Context, cred Credentials) error {
	m.Lock()
	defer m.Unlock()
	delete(m.creds, cred.Host)
	return nil
}

func (m *memoryStore) Get(ctx context.Context, request *transfertypes.AuthRequest) (*transfertypes.AuthResponse, error) {
	m.RLock()
	defer m.RUnlock()
	c, ok := m.creds[request.Host]
	if !ok {
		return &transfertypes.AuthResponse{}, fmt.Errorf("creds not found for %s", request.Host)
	}
	resp := &transfertypes.AuthResponse{}
	if c.Header != "" {
		resp.AuthType = transfertypes.AuthType_HEADER
		resp.Secret = c.Header
	} else if c.Username != "" {
		resp.AuthType = transfertypes.AuthType_CREDENTIALS
		resp.Username = c.Username
		resp.Secret = c.Secret
	} else {
		resp.AuthType = transfertypes.AuthType_REFRESH
		resp.Secret = c.Secret
	}
	return resp, nil
}
