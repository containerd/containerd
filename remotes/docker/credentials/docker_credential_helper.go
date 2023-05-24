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

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// dockerCredentialHelper provides implementation for a credential helper that's compatible to the
// docker-credential-helpers (https://github.com/docker/docker-credential-helpers) executables.
type dockerCredentialHelper struct {
	path string
}

func (p *dockerCredentialHelper) Get(serverURL string) (*Credentials, error) {
	in := strings.NewReader(serverURL)
	response, err := p.exec(in, "get")
	if err != nil {
		return nil, err
	}

	var credentials Credentials
	if err = json.NewDecoder(bytes.NewReader(response)).Decode(&credentials); err != nil {
		return nil, fmt.Errorf("failed to decode response %w", err)
	}

	return &credentials, nil
}

func (p *dockerCredentialHelper) Set(credentials *Credentials) error {
	buffer := new(bytes.Buffer)
	if err := json.NewEncoder(buffer).Encode(credentials); err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}

	_, err := p.exec(buffer, "store")
	return err
}

func (p *dockerCredentialHelper) Delete(serverURL string) error {
	in := strings.NewReader(serverURL)
	_, err := p.exec(in, "erase")
	return err
}

func (p *dockerCredentialHelper) List() ([]string, error) {
	in := strings.NewReader("")
	response, err := p.exec(in, "list")
	if err != nil {
		return nil, err
	}

	var credentials map[string]string
	if err = json.NewDecoder(bytes.NewReader(response)).Decode(&credentials); err != nil {
		return nil, fmt.Errorf("failed to decode response %w", err)
	}

	var servers []string
	for url := range credentials {
		servers = append(servers, url)
	}

	return servers, nil
}

func (p *dockerCredentialHelper) exec(in io.Reader, args ...string) ([]byte, error) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	cmd := exec.Command(p.path, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = in
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to execute %s %s: error: %w, stderr: %s", p.path, args, err, stderr)
	}

	return stdout.Bytes(), nil
}
