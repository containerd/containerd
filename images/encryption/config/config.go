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

// EncryptConfig is the container image PGP encryption configuration holding
// the identifiers of those that will be able to decrypt the container and
// the PGP public keyring file data that contains their public keys.
type EncryptConfig struct {
	// map holding 'gpg-recipients', 'gpg-pubkeyringfile', 'pubkeys', 'x509s'
	Parameters map[string][][]byte

	DecryptConfig DecryptConfig
}

// DecryptConfig wraps the Parameters map that holds the decryption key
type DecryptConfig struct {
	// map holding 'privkeys', 'x509s', 'gpg-privatekeys'
	Parameters map[string][][]byte
}

// CryptoConfig is a common wrapper for EncryptConfig and DecrypConfig that can
// be passed through functions that share much code for encryption and decryption
type CryptoConfig struct {
	EncryptConfig *EncryptConfig
	DecryptConfig *DecryptConfig
}
