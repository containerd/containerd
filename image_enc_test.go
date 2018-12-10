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

package containerd

import (
	"context"
	"runtime"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/encryption"
	encconfig "github.com/containerd/containerd/images/encryption/config"
	"github.com/containerd/containerd/platforms"
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

func setupBusyboxImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}

	const imageName = "docker.io/library/busybox:latest"
	ctx, cancel := testContext()
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// Cleanup
	err = client.ImageService().Delete(ctx, imageName)
	if err != nil && !errdefs.IsNotFound(err) {
		t.Fatal(err)
	}

	// By default pull does not unpack an image
	image, err := client.Pull(ctx, imageName, WithPlatform("linux/amd64"))
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(ctx, DefaultSnapshotter)
	if err != nil {
		t.Fatal(err)
	}
}

func TestImageEncryption(t *testing.T) {
	setupBusyboxImage(t)

	const imageName = "docker.io/library/busybox:latest"
	ctx, cancel := testContext()
	defer cancel()

	client, err := newClient(t, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	s := client.ImageService()

	image, err := s.Get(ctx, imageName)
	if err != nil {
		t.Fatal(err)
	}

	pl, err := platforms.ParseArray([]string{"linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}

	lf := &encryption.LayerFilter{
		Layers:    []int32{},
		Platforms: pl,
	}

	dcparameters := make(map[string][][]byte)
	parameters := make(map[string][][]byte)

	parameters["pubkeys"] = [][]byte{publicKey}
	dcparameters["privkeys"] = [][]byte{privateKey}
	dcparameters["privkeys-passwords"] = [][]byte{{}}

	cc := &encconfig.CryptoConfig{
		Ec: &encconfig.EncryptConfig{
			Parameters: parameters,
			Operation:  encconfig.OperationAddRecipients,
			Dc: encconfig.DecryptConfig{
				Parameters: dcparameters,
			},
		},
	}

	// Perform encryption of image
	encSpec, modified, err := images.EncryptImage(ctx, client.ContentStore(), image.Target, cc, lf)
	if err != nil {
		t.Fatal(err)
	}
	if !modified || image.Target.Digest == encSpec.Digest {
		t.Fatal("Encryption did not modify the spec")
	}

	if !hasEncryption(ctx, client.ContentStore(), encSpec) {
		t.Fatal("Encrypted image does not have encrypted layers")
	}

	cc = &encconfig.CryptoConfig{
		Dc: &encconfig.DecryptConfig{
			Parameters: dcparameters,
		},
	}

	// Perform decryption of image
	defer client.ImageService().Delete(ctx, imageName, images.SynchronousDelete())
	decSpec, modified, err := images.DecryptImage(ctx, client.ContentStore(), encSpec, cc, lf)
	if err != nil {
		t.Fatal(err)
	}
	if !modified || encSpec.Digest == decSpec.Digest {
		t.Fatal("Decryption did not modify the spec")
	}

	if hasEncryption(ctx, client.ContentStore(), decSpec) {
		t.Fatal("Decrypted image has encrypted layers")
	}
}

func hasEncryption(ctx context.Context, provider content.Provider, spec ocispec.Descriptor) bool {
	switch spec.MediaType {
	case images.MediaTypeDockerSchema2LayerEnc, images.MediaTypeDockerSchema2LayerGzipEnc:
		return true
	default:
		// pass
	}
	cspecs, err := images.Children(ctx, provider, spec)
	if err != nil {
		return false
	}

	for _, v := range cspecs {
		if hasEncryption(ctx, provider, v) {
			return true
		}
	}

	return false
}
