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

package server

import (
	"testing"

	"github.com/containerd/containerd/v2/pkg/cri/config"
	"github.com/stretchr/testify/assert"
)

func TestValidateStreamServer(t *testing.T) {
	for _, test := range []struct {
		desc string
		*criService
		tlsMode   streamListenerMode
		expectErr bool
	}{
		{
			desc: "should pass with default withoutTLS",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.DefaultConfig(),
				},
			},
			tlsMode:   withoutTLS,
			expectErr: false,
		},
		{
			desc: "should pass with x509KeyPairTLS",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: true,
						X509KeyPairStreaming: config.X509KeyPairStreaming{
							TLSKeyFile:  "non-empty",
							TLSCertFile: "non-empty",
						},
					},
				},
			},
			tlsMode:   x509KeyPairTLS,
			expectErr: false,
		},
		{
			desc: "should pass with selfSign",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: true,
					},
				},
			},
			tlsMode:   selfSignTLS,
			expectErr: false,
		},
		{
			desc: "should return error with X509 keypair but not EnableTLSStreaming",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: false,
						X509KeyPairStreaming: config.X509KeyPairStreaming{
							TLSKeyFile:  "non-empty",
							TLSCertFile: "non-empty",
						},
					},
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error with X509 TLSCertFile empty",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: true,
						X509KeyPairStreaming: config.X509KeyPairStreaming{
							TLSKeyFile:  "non-empty",
							TLSCertFile: "",
						},
					},
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error with X509 TLSKeyFile empty",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: true,
						X509KeyPairStreaming: config.X509KeyPairStreaming{
							TLSKeyFile:  "",
							TLSCertFile: "non-empty",
						},
					},
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error without EnableTLSStreaming and only TLSCertFile set",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: false,
						X509KeyPairStreaming: config.X509KeyPairStreaming{
							TLSKeyFile:  "",
							TLSCertFile: "non-empty",
						},
					},
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
		{
			desc: "should return error without EnableTLSStreaming and only TLSKeyFile set",
			criService: &criService{
				config: config.Config{
					PluginConfig: config.PluginConfig{
						EnableTLSStreaming: false,
						X509KeyPairStreaming: config.X509KeyPairStreaming{
							TLSKeyFile:  "non-empty",
							TLSCertFile: "",
						},
					},
				},
			},
			tlsMode:   -1,
			expectErr: true,
		},
	} {
		test := test
		t.Run(test.desc, func(t *testing.T) {
			tlsMode, err := getStreamListenerMode(test.criService)
			if test.expectErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, test.tlsMode, tlsMode)
		})
	}
}
