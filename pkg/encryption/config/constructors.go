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

// NewJweCryptoConfig returns a CryptoConfig that contains the required configuration for using
// the jwe keyunwrap interface
func NewJweCryptoConfig(pubKey *[]byte, privKey *[]byte, privKeyPassword *string) CryptoConfig {
	pubKeys := [][]byte{}
	privKeys := [][]byte{}
	privKeysPasswords := [][]byte{}

	if pubKey != nil {
		pubKeys = append(pubKeys, *pubKey)
	}
	if privKey != nil {
		privKeys = append(privKeys, *privKey)
	}
	if privKeyPassword != nil {
		privKeysPasswords = append(privKeysPasswords, []byte(*privKeyPassword))
	}

	dc := DecryptConfig{
		Parameters: map[string][][]byte{
			"privkeys":           privKeys,
			"privkeys-passwords": privKeysPasswords,
		},
	}

	ep := map[string][][]byte{
		"pubkeys": pubKeys,
	}

	return CryptoConfig{
		EncryptConfig: &EncryptConfig{
			Parameters:    ep,
			DecryptConfig: dc,
		},
		DecryptConfig: &dc,
	}
}

// NewPkcs7CryptoConfig returns a CryptoConfig that contains the required configuration for using
// the pkcs7 keyunwrap interface
func NewPkcs7CryptoConfig(x509 *[]byte, privKey *[]byte, privKeyPassword *string) CryptoConfig {
	x509s := [][]byte{}
	privKeys := [][]byte{}
	privKeysPasswords := [][]byte{}

	if x509 != nil {
		x509s = append(x509s, *x509)
	}
	if privKey != nil {
		privKeys = append(privKeys, *privKey)
	}
	if privKeyPassword != nil {
		privKeysPasswords = append(privKeysPasswords, []byte(*privKeyPassword))
	}

	dc := DecryptConfig{
		Parameters: map[string][][]byte{
			"privkeys":           privKeys,
			"privkeys-passwords": privKeysPasswords,
		},
	}

	ep := map[string][][]byte{
		"x509s": x509s,
	}

	return CryptoConfig{
		EncryptConfig: &EncryptConfig{
			Parameters:    ep,
			DecryptConfig: dc,
		},
		DecryptConfig: &dc,
	}
}
