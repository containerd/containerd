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

	transfertypes "github.com/containerd/containerd/v2/api/types/transfer"
)

type CredsStoreType string

const (
	// KEYCHAIN is macOS
	KEYCHAIN CredsStoreType = "osxkeychain"
	// WinCred  on windows
	WinCred CredsStoreType = "wincred"
	// PASS on Linux
	PASS CredsStoreType = "pass"
	// SecretService is  if it cannot find the "pass"
	SecretService CredsStoreType = "secretservice"
	// FILE is base64 encoding in the config files
	FILE CredsStoreType = "file"
	// MEMORY is base64 encoding in the memory
	MEMORY CredsStoreType = "memory"
)

func (cst CredsStoreType) String() string {
	return string(cst)
}

type Credentials struct {
	Host     string
	Username string
	Secret   string
	Header   string
}

// Manager is Credential manager plugin core method
type Manager interface {
	Store(ctx context.Context, cred Credentials) error
	Get(ctx context.Context, request *transfertypes.AuthRequest) (*transfertypes.AuthResponse, error)
	Delete(ctx context.Context, cred Credentials) error
}

type Auth interface {
	Get(request transfertypes.AuthRequest) transfertypes.AuthResponse
}

func NewManager(csType string) Manager {
	if csType == KEYCHAIN.String() || csType == WinCred.String() || csType == PASS.String() || csType == SecretService.String() {
		return newDockerCredential(csType)
	}
	if csType == MEMORY.String() {
		return newMemoryStore()
	}
	return newFileStore()
}
