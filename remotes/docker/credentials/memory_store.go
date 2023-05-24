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

package credentials

import "fmt"

// MemoryStore is an in-memory credentials store implementing the CredentialHelper interface.
// Used primarily for testing purpose.
type MemoryStore struct {
	credentials map[string]*Credentials
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		credentials: make(map[string]*Credentials),
	}
}

func (m *MemoryStore) Get(serverURL string) (*Credentials, error) {
	c, ok := m.credentials[serverURL]
	if !ok {
		return nil, fmt.Errorf("no credentials found for %s", serverURL)
	}
	return c, nil
}

func (m *MemoryStore) Set(credentials *Credentials) error {
	if credentials.ServerURL == "" {
		return fmt.Errorf("server must be set in credentials")
	}

	m.credentials[credentials.ServerURL] = credentials
	return nil
}

func (m *MemoryStore) Delete(serverURL string) error {
	delete(m.credentials, serverURL)
	return nil
}

func (m *MemoryStore) List() ([]string, error) {
	var servers []string
	for url := range m.credentials {
		servers = append(servers, url)
	}
	return servers, nil
}
