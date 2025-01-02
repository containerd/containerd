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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateStreamServer(t *testing.T) {
	for _, test := range []struct {
		desc      string
		config    ServerConfig
		tlsMode   streamListenerMode
		expectErr bool
	}{
		{
			desc:      "should pass with default withoutTLS",
			config:    DefaultServerConfig(),
			tlsMode:   withoutTLS,
			expectErr: false,
		},
		{
			desc: "should pass with x509KeyPairTLS",
			config: ServerConfig{
				EnableTLSStreaming: true,
				X509KeyPairStreaming: X509KeyPairStreaming{
					TLSKeyFile:  "non-empty",
					TLSCertFile: "non-empty",
				},
			},
			tlsMode:   x509KeyPairTLS,
			expectErr: false,
		},
		{
			desc: "should pass with selfSign",
			config: ServerConfig{
				EnableTLSStreaming: true,
			},
			tlsMode:   selfSignTLS,
			expectErr: false,
		},
		{
			desc: "should return error with X509 keypair but not EnableTLSStreaming",
			config: ServerConfig{
				EnableTLSStreaming: false,
				X509KeyPairStreaming: X509KeyPairStreaming{
					TLSKeyFile:  "non-empty",
					TLSCertFile: "non-empty",
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error with X509 TLSCertFile empty",
			config: ServerConfig{
				EnableTLSStreaming: true,
				X509KeyPairStreaming: X509KeyPairStreaming{
					TLSKeyFile:  "non-empty",
					TLSCertFile: "",
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error with X509 TLSKeyFile empty",
			config: ServerConfig{
				EnableTLSStreaming: true,
				X509KeyPairStreaming: X509KeyPairStreaming{
					TLSKeyFile:  "",
					TLSCertFile: "non-empty",
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error without EnableTLSStreaming and only TLSCertFile set",
			config: ServerConfig{
				EnableTLSStreaming: false,
				X509KeyPairStreaming: X509KeyPairStreaming{
					TLSKeyFile:  "",
					TLSCertFile: "non-empty",
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error without EnableTLSStreaming and only TLSKeyFile set",
			config: ServerConfig{
				EnableTLSStreaming: false,
				X509KeyPairStreaming: X509KeyPairStreaming{
					TLSKeyFile:  "non-empty",
					TLSCertFile: "",
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			tlsMode, err := getStreamListenerMode(&test.config)
			if test.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, test.tlsMode, tlsMode)
		})
	}
}
