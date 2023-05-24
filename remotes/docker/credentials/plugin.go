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

// Package credentials contains the interface for managing credentials from plugins
// and client implementations for supported types of credential helpers.
package credentials

import (
	"fmt"
	"strings"
)

type Credentials struct {
	// ServerURL is the host name of the server
	ServerURL string
	// Username is the username used to authenticate to the server
	Username string
	// Secret is the secret for the username to authenticate to the server
	Secret string
	// Header is the authorization value to be set in the header for the http request
	Header string
}

type CredentialHelper interface {
	// Get retrieves credentials to be used for the server from the helper.
	Get(serverURL string) (*Credentials, error)
	// Set persists the credentials for a particular server to the helper.
	Set(credentials *Credentials) error
	// Delete removes the credentials for a particular server from the helper.
	Delete(serverURL string) error
	// List returns a list of server URLs that have credentials persisted in the helper.
	List() ([]string, error)
}

// GetCredentialHelper checks the type of the credential helper to ensure that it is
// supported in containerd and returns the CredentialHelper provider for supported type.
var GetCredentialHelper = func(path, helperType string) (CredentialHelper, error) {
	switch strings.ToLower(helperType) {
	case "docker-credential-helper":
		return &dockerCredentialHelper{
			path: path,
		}, nil
	default:
		return nil, fmt.Errorf("supported credential helper type: %s", helperType)
	}
}
