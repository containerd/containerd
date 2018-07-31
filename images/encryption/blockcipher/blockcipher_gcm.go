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

package blockcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"

	"github.com/pkg/errors"
)

// GCMLayerBlockCipher implements the AEAD GCM block cipher with AES
type GCMLayerBlockCipher struct {
	bits int // 128, 256, etc.
}

// NewGCMLayerBlockCipher returns a new GCM block cipher of 128 or 256 bits
func NewGCMLayerBlockCipher(bits int) (LayerBlockCipher, error) {
	if bits != 128 && bits != 256 {
		return nil, errors.New("GCM bit count not supported")
	}
	return &GCMLayerBlockCipher{bits: bits}, nil
}

// Encrypt takes in layer data and returns the ciphertext and relevant LayerBlockCipherOptions
func (bc *GCMLayerBlockCipher) Encrypt(layerData []byte, opt LayerBlockCipherOptions) ([]byte, LayerBlockCipherOptions, error) {
	key := opt.SymmetricKey
	plaintext := layerData

	if len(key) != bc.bits/8 {
		return nil, LayerBlockCipherOptions{}, errors.New("Invalid key length")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to AES generate block cipher")
	}

	// Never use more than 2^32 random nonces with a given key because of the risk of a repeat.
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to generate random nonce")
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to create new GCM object")
	}

	ciphertext := aesgcm.Seal(nil, nonce, plaintext, nil)

	lbco := LayerBlockCipherOptions{
		SymmetricKey: key,
		CipherOptions: map[string]string{
			"nonce": base64.StdEncoding.EncodeToString(nonce),
		},
	}
	return ciphertext, lbco, nil
}

// Decrypt takes in layer ciphertext data and returns the plaintext and relevant LayerBlockCipherOptions
func (bc *GCMLayerBlockCipher) Decrypt(layerData []byte, opt LayerBlockCipherOptions) ([]byte, LayerBlockCipherOptions, error) {
	key := opt.SymmetricKey
	ciphertext := layerData
	nonceStr := opt.CipherOptions["nonce"]

	var nonce []byte
	var err error
	if nonceStr != "" {
		// Decode nonce str
		nonce, err = base64.StdEncoding.DecodeString(nonceStr)
		if err != nil {
			return nil, LayerBlockCipherOptions{}, errors.New("Failed to decode nonce")
		}
	}

	if len(key) != bc.bits/8 {
		return nil, LayerBlockCipherOptions{}, errors.New("Invalid key length")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to AES generate block cipher")
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to create new GCM object")
	}

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to decrypt ciphertext")
	}

	return plaintext, LayerBlockCipherOptions{}, nil
}
