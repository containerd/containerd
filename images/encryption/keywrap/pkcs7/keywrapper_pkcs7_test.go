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

package pkcs7

import (
	"crypto/x509"
	"testing"

	"github.com/containerd/containerd/images/encryption/config"
	"github.com/containerd/containerd/images/encryption/utils"
)

var oneEmpty []byte

func createKeys() (*x509.Certificate, []byte, *x509.Certificate, []byte, error) {
	caKey, caCert, err := utils.CreateTestCA()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	pkcs7ClientPubKey, pkcs7ClientPrivKey, err := utils.CreateRSATestKey(2048, oneEmpty, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	pkcs7ClientCert, err := utils.CertifyKey(pkcs7ClientPubKey, nil, caKey, caCert)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	pkcs7ClientPubKey2, pkcs7ClientPrivKey2, err := utils.CreateRSATestKey(2048, oneEmpty, true)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	pkcs7ClientCert2, err := utils.CertifyKey(pkcs7ClientPubKey2, nil, caKey, caCert)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return pkcs7ClientCert, pkcs7ClientPrivKey, pkcs7ClientCert2, pkcs7ClientPrivKey2, nil
}

func createValidPkcs7Ccs() ([]*config.CryptoConfig, error) {
	pkcs7ClientCert, pkcs7ClientPrivKey, pkcs7ClientCert2, pkcs7ClientPrivKey2, err := createKeys()
	if err != nil {
		return nil, err
	}

	validPkcs7Ccs := []*config.CryptoConfig{
		// Client key 1
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientCert.Raw},
				},
				DecryptConfig: config.DecryptConfig{
					Parameters: map[string][][]byte{
						"privkeys":           {pkcs7ClientPrivKey},
						"privkeys-passwords": {oneEmpty},
						"x509s":              {pkcs7ClientCert.Raw},
					},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {pkcs7ClientPrivKey},
					"privkeys-passwords": {oneEmpty},
					"x509s":              {pkcs7ClientCert.Raw},
				},
			},
		},

		// Client key 2
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientCert2.Raw},
				},
				DecryptConfig: config.DecryptConfig{
					Parameters: map[string][][]byte{
						"privkeys":           {pkcs7ClientPrivKey2},
						"privkeys-passwords": {oneEmpty},
						"x509s":              {pkcs7ClientCert2.Raw},
					},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {pkcs7ClientPrivKey2},
					"privkeys-passwords": {oneEmpty},
					"x509s":              {pkcs7ClientCert2.Raw},
				},
			},
		},

		// Client key 1 without enc private key
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientCert.Raw},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {pkcs7ClientPrivKey},
					"privkeys-passwords": {oneEmpty},
					"x509s":              {pkcs7ClientCert.Raw},
				},
			},
		},

		// Client key 2 without enc private key
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientCert2.Raw},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {pkcs7ClientPrivKey2},
					"privkeys-passwords": {oneEmpty},
					"x509s":              {pkcs7ClientCert2.Raw},
				},
			},
		},
	}
	return validPkcs7Ccs, nil
}

func createInvalidPkcs7Ccs() ([]*config.CryptoConfig, error) {
	pkcs7ClientCert, pkcs7ClientPrivKey, pkcs7ClientCert2, pkcs7ClientPrivKey2, err := createKeys()
	if err != nil {
		return nil, err
	}

	invalidPkcs7Ccs := []*config.CryptoConfig{
		// Client key 1 public with client 2 private decrypt
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientCert.Raw},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {pkcs7ClientPrivKey2},
					"privkeys-passwords": {oneEmpty},
					"x509s":              {pkcs7ClientCert2.Raw},
				},
			},
		},

		// Client key 1 public with no private key
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientCert.Raw},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{},
			},
		},

		// Invalid Client key 1 private key
		{
			EncryptConfig: &config.EncryptConfig{
				Parameters: map[string][][]byte{
					"x509s": {pkcs7ClientPrivKey},
				},
			},
			DecryptConfig: &config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {pkcs7ClientCert.Raw},
					"privkeys-passwords": {oneEmpty},
					"x509s":              {pkcs7ClientCert.Raw},
				},
			},
		},
	}
	return invalidPkcs7Ccs, nil
}

func TestKeyWrapPkcs7Success(t *testing.T) {
	validPkcs7Ccs, err := createValidPkcs7Ccs()
	if err != nil {
		t.Fatal(err)
	}

	for _, cc := range validPkcs7Ccs {
		kw := NewKeyWrapper()

		data := []byte("This is some secret text")

		wk, err := kw.WrapKeys(cc.EncryptConfig, data)
		if err != nil {
			t.Fatal(err)
		}

		ud, err := kw.UnwrapKey(cc.DecryptConfig, wk)
		if err != nil {
			t.Fatal(err)
		}

		if string(data) != string(ud) {
			t.Fatal("Strings don't match")
		}
	}
}

func TestKeyWrapPkcs7Invalid(t *testing.T) {
	invalidPkcs7Ccs, err := createInvalidPkcs7Ccs()
	if err != nil {
		t.Fatal(err)
	}

	for _, cc := range invalidPkcs7Ccs {
		kw := NewKeyWrapper()

		data := []byte("This is some secret text")

		wk, err := kw.WrapKeys(cc.EncryptConfig, data)
		if err != nil {
			return
		}

		ud, err := kw.UnwrapKey(cc.DecryptConfig, wk)
		if err != nil {
			return
		}

		if string(data) != string(ud) {
			return
		}

		t.Fatal("Successfully wrap for invalid crypto config")
	}
}
