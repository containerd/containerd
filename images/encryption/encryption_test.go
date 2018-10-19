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

package encryption

import (
	"reflect"
	"testing"

	"github.com/containerd/containerd/images/encryption/config"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

var (
	privateKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAnnExCL+jpTAgZzgEBwmR88ltusMBdsd4ZDO4M5NohTq0T6TP
vJN99ud8ZAY8fVpN63TKD2enWy6imkSE3Cdp+yFgOntsg8WgdPF+8VQaNpn/g8LC
rpWXpJuJIGzCSW5SlUt0OkvyeDvQFrKCqdI63H4cxXY5ly2HlTHJ1+YzEBSLONJY
xsJt6/7cL7mJ7CR/9NZouft2xeNQto9JSqWUNCwUGdZOS1pMVkka/2tFmq8psciF
YvCl6msX6haZ7IDq4/GfeteL8gp6t+hgyWmbJ5/h0hNEvAz4DVb5RnKYVLwWM/e9
oQTw9WgKCqUZKe0+DKmuKYMH2g77oTvDtP8NvQIDAQABAoIBAC8tUQZj2ZxEGkHh
wgE+bkECxzOHARaXClf7tmtVBxg0hJ/6WQizehxcjQNTgAtrKixj2A6CNKjH2A7L
PCw5aCsooviG66bI36AykDPXcP61GAnpogJN9JtE3K3U9Hzc5qYhk3gQSSBX3vwD
Jzjdqj0hJ/v72eYT3n0kGA+7MZUlsObpOouPAZMo72Bcvg2s20FLnKQCiGfH8zWv
wJAnO5BhinwTPhi+01Xj9LePk/2bs3hEzH1/bA3DVmmaWp5H8RuaGuvQ6eX4EXir
3xq9BjjYIK21dmD2R1S0jjez3/d2P7gENKGVItcakURWIn7IS0bYr8P2xIhnxQQV
OTYgWRkCgYEAyboK1GDr+5KtCoAQE1e1Ntbi+x3R2sIjX8lGzLumd5ybqSMGH8V9
6efRo7onuP5V66bDwxSeSFomOEF0rQZj3dpoEXkF95h8G65899okXMURsqjb6+wm
xyFKZAJojJXsR076ES3tas/TgPVD/ZfcBYTU8Ssvfsi3uzeUrbuVL58CgYEAyRHq
/1zsPDf3B7E8EsRA/YZJTfjzDlqXatTX5dnoUKBQH9nZut4qGWBwWPj2DKJwlGQr
m12RIbM5uGvUe3csddzClp0zInDhvD/K3XlUthUfrYX209xaeOD6d4+7wd56SNEo
AzhSobgmrITEAy8QA1u546ID+gFOQnzG17HelSMCgYEAsdmoep4I7//dOAi4I5WM
WxERhTxBLJFFBso59X7rwUD9rB0I5TIFVRfhKGyTYPI7ZkvdBD1FX5y7XZW3/GRJ
3+sTHXSJ4kU6Bl3MJ+jXbkMA23csjc/iUGX1ZD8LVgdIDYZ/ym2niCg63NNgYlBk
1yjJZOciNLFZ62GRX6qmWRkCgYAYS7j4mFLXR+/qlwjqP5qWx9YtvMopztqDByr7
VCRVMbncz2cWxGeT32pT5eldR3eRBrWaNWknCFAOL8FiFdlieIVuy5n1LGyqYY7y
yglpYw4L2qcjnHm2J4E8VzrZxzdBezx5fyHE9sp9iCFjPRmTPk8s6VPPrr61G/yu
7Yg2vwKBgAsJFi6zjqfUacMxB+Bb4Ehz7bqRAoeHZCH9MU2lGimjTUC322uQdu2H
ZkYkHwYVP/RngrN7bhYwPahoDThKy+fIGJAuMhPXg6HVTSkcQSJJ/VeIB/DE4AVj
8heezMN183u5gJvwaEj84fJvUEo/QdvG3NSjQptEGsXYSsE56wHu
-----END RSA PRIVATE KEY-----`)

	publicKey = []byte(`-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAnnExCL+jpTAgZzgEBwmR
88ltusMBdsd4ZDO4M5NohTq0T6TPvJN99ud8ZAY8fVpN63TKD2enWy6imkSE3Cdp
+yFgOntsg8WgdPF+8VQaNpn/g8LCrpWXpJuJIGzCSW5SlUt0OkvyeDvQFrKCqdI6
3H4cxXY5ly2HlTHJ1+YzEBSLONJYxsJt6/7cL7mJ7CR/9NZouft2xeNQto9JSqWU
NCwUGdZOS1pMVkka/2tFmq8psciFYvCl6msX6haZ7IDq4/GfeteL8gp6t+hgyWmb
J5/h0hNEvAz4DVb5RnKYVLwWM/e9oQTw9WgKCqUZKe0+DKmuKYMH2g77oTvDtP8N
vQIDAQAB
-----END PUBLIC KEY-----`)
)

var (
	ec *config.EncryptConfig
	dc *config.DecryptConfig
)

func init() {
	// TODO: Create various EncryptConfigs for testing purposes
	dcparameters := make(map[string][][]byte)
	parameters := make(map[string][][]byte)

	parameters["pubkeys"] = [][]byte{publicKey}
	dcparameters["privkeys"] = [][]byte{privateKey}
	dcparameters["privkeys-passwords"] = [][]byte{{}}

	ec = &config.EncryptConfig{
		Parameters: parameters,
		Operation:  config.OperationAddRecipients,
		Dc: config.DecryptConfig{
			Parameters: dcparameters,
		},
	}
	dc = &config.DecryptConfig{
		Parameters: dcparameters,
	}
}

func TestEncryptLayer(t *testing.T) {
	data := []byte("This is some text!")
	desc := ocispec.Descriptor{
		Digest: digest.FromBytes(data),
		Size:   int64(len(data)),
	}

	encLayer, annotations, err := EncryptLayer(ec, data, desc)
	if err != nil {
		t.Fatal(err)
	}

	if len(annotations) == 0 {
		t.Fatal("No keys created for annotations")
	}

	newDesc := ocispec.Descriptor{
		Annotations: annotations,
		Digest:      digest.FromBytes(encLayer),
		Size:        int64(len(encLayer)),
	}

	decLayer, err := DecryptLayer(dc, encLayer, newDesc, false)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(decLayer, data) {
		t.Fatalf("Expected %v, got %v", data, decLayer)
	}
}
