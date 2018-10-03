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

package jwe

import (
	"testing"

	"github.com/containerd/containerd/images/encryption/config"
)

var oneEmpty []byte

var validJweCcs = []*config.CryptoConfig{
	// Key 1
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyPem},
			},
			Operation: config.OperationAddRecipients,
			Dc: config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {jwePrivKeyPem},
					"privkeys-passwords": {oneEmpty},
				},
			},
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKeyPem},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},

	// Key 2
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKey2Pem},
			},
			Operation: config.OperationAddRecipients,
			Dc: config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {jwePrivKey2Pem},
					"privkeys-passwords": {oneEmpty},
				},
			},
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKey2Pem},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},

	// Key 1 without enc private key
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyPem},
			},
			Operation: config.OperationAddRecipients,
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKeyPem},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},

	// Key 2 without enc private key
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKey2Pem},
			},
			Operation: config.OperationAddRecipients,
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKey2Pem},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},

	// Key 3 with enc private key
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKey3Pem},
			},
			Operation: config.OperationAddRecipients,
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKey3PassPem},
				"privkeys-passwords": {jwePrivKey3Password},
			},
		},
	},

	// Key (DER format)
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyDer},
			},
			Operation: config.OperationAddRecipients,
			Dc: config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {jwePrivKeyDer},
					"privkeys-passwords": {oneEmpty},
				},
			},
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKeyDer},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},
	// Key (JWK format)
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyJwk},
			},
			Operation: config.OperationAddRecipients,
			Dc: config.DecryptConfig{
				Parameters: map[string][][]byte{
					"privkeys":           {jwePrivKeyJwk},
					"privkeys-passwords": {oneEmpty},
				},
			},
		},

		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePrivKeyJwk},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},
}

var invalidJweCcs = []*config.CryptoConfig{
	// Client key 1 public with client 2 private decrypt
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyPem},
			},
			Operation: config.OperationAddRecipients,
		},
		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePubKey2Pem},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},

	// Client key 1 public with no private key
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyPem},
			},
			Operation: config.OperationAddRecipients,
		},
		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{},
		},
	},

	// Invalid Client key 1 private key
	{
		Ec: &config.EncryptConfig{
			Parameters: map[string][][]byte{
				"pubkeys": {jwePubKeyPem},
			},
			Operation: config.OperationAddRecipients,
		},
		Dc: &config.DecryptConfig{
			Parameters: map[string][][]byte{
				"privkeys":           {jwePubKeyPem},
				"privkeys-passwords": {oneEmpty},
			},
		},
	},
}

func TestKeyWrapJweSuccess(t *testing.T) {
	for _, cc := range validJweCcs {
		kw := NewKeyWrapper()

		data := []byte("This is some secret text")

		wk, err := kw.WrapKeys(cc.Ec, data)
		if err != nil {
			t.Fatal(err)
		}

		ud, err := kw.UnwrapKey(cc.Dc, wk)
		if err != nil {
			t.Fatal(err)
		}

		if string(data) != string(ud) {
			t.Fatal("Strings don't match")
		}
	}
}

func TestKeyWrapJweInvalid(t *testing.T) {
	for _, cc := range invalidJweCcs {
		kw := NewKeyWrapper()

		data := []byte("This is some secret text")

		wk, err := kw.WrapKeys(cc.Ec, data)
		if err != nil {
			return
		}

		ud, err := kw.UnwrapKey(cc.Dc, wk)
		if err != nil {
			return
		}

		if string(data) != string(ud) {
			return
		}

		t.Fatal("Successfully wrap for invalid crypto config")
	}
}
