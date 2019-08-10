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

package certutil

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/pkg/errors"
)

// SystemCertPool returns a copy of the system cert pool,
// returns an error if failed to load or empty pool on windows.
//
// SystemCertPool was ported from Docker 19.03
// https://github.com/docker/engine/blob/v19.03.1/vendor/github.com/docker/go-connections/tlsconfig/certpool_go17.go#L12
func SystemCertPool() (*x509.CertPool, error) {
	certpool, err := x509.SystemCertPool()
	if err != nil && runtime.GOOS == "windows" {
		return x509.NewCertPool(), nil
	}
	return certpool, err
}

// LoadCACerts loads CA certificates into tlsConfig from glob.
// glob should be like "/etc/docker/certs.d/example.com/*.crt" .
// LoadCACerts returns the paths of the loaded certs.
func LoadCACerts(tlsConfig *tls.Config, glob string) ([]string, error) {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if tlsConfig.RootCAs == nil {
		systemPool, err := SystemCertPool()
		if err != nil {
			return nil, errors.Wrap(err, "unable to get system cert poolv")
		}
		tlsConfig.RootCAs = systemPool
	}
	var loaded []string
	for _, f := range files {
		data, err := ioutil.ReadFile(f)
		if err != nil {
			return loaded, errors.Wrapf(err, "unable to read CA cert %q", f)
		}
		if !tlsConfig.RootCAs.AppendCertsFromPEM(data) {
			return loaded, errors.Errorf("unable to load CA cert %q", f)
		}
		loaded = append(loaded, f)
	}
	return loaded, nil
}

// KeyPairCertLocator is used to resolve the cert path from the key path.
type KeyPairCertLocator func(keyPath string) (certPath string, err error)

// DockerKeyPairCertLocator implements the Docker-style convention. ("*.key" -> "*.cert")
var DockerKeyPairCertLocator KeyPairCertLocator = func(keyPath string) (string, error) {
	if !strings.HasSuffix(keyPath, ".key") {
		return "", errors.Errorf("expected key path with \".key\" suffix, got %q", keyPath)
	}
	// Docker convention uses *.crt for CA certs, *.cert for keypair certs.
	certPath := keyPath[:len(keyPath)-4] + ".cert"
	return certPath, nil
}

// LoadKeyPairs loads key pairs into tlsConfig from keyGlob.
// keyGlob should be like "/etc/docker/certs.d/example.com/*.key" .
// certLocator is used to resolve the cert path from the key path.
// Use DockerKeyPairCertLocator for the Docker-style convention. ("*.key" -> "*.cert")
// LoadKeyParis returns the paths of the loaded certs and the loaded keys.
func LoadKeyPairs(tlsConfig *tls.Config, keyGlob string, certLocator KeyPairCertLocator) ([]string, []string, error) {
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	if certLocator == nil {
		return nil, nil, errors.Wrap(errdefs.ErrInvalidArgument, "missing cert locator")
	}
	keyPaths, err := filepath.Glob(keyGlob)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(keyPaths)
	var (
		loadedCerts []string
		loadedKeys  []string
	)
	for _, keyPath := range keyPaths {
		certPath, err := certLocator(keyPath)
		if err != nil {
			return loadedCerts, loadedKeys, err
		}
		keyPair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return loadedCerts, loadedKeys, err
		}
		tlsConfig.Certificates = append(tlsConfig.Certificates, keyPair)
		loadedCerts = append(loadedCerts, certPath)
		loadedKeys = append(loadedKeys, keyPath)
	}
	return loadedCerts, loadedKeys, nil
}
