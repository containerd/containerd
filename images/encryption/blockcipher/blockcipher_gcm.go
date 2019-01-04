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
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"hash"
	"io"

	"github.com/containerd/containerd/content"

	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// GCMLayerBlockCipher implements the AEAD GCM block cipher with AES
type GCMLayerBlockCipher struct {
	bits   int // 128, 256, etc.
	key    []byte
	nonce  []byte
	reader content.ReaderAt
}

// GCMEncryptor implments the CryptedDataReader for en- and decrypting input
// data and calculating the hash over the data
type GCMEncryptor interface {
	CryptedDataReader
}

type gcmcryptor struct {
	encrypt      bool
	bc           *GCMLayerBlockCipher
	outputReader io.Reader
	digester     digest.Digester
	size         int64
}

// NewGCMLayerBlockCipher returns a new GCM block cipher of 128 or 256 bits
func NewGCMLayerBlockCipher(bits int) (LayerBlockCipher, error) {
	if bits != 128 && bits != 256 {
		return nil, errors.New("GCM bit count not supported")
	}
	return &GCMLayerBlockCipher{bits: bits}, nil
}

// crypt encrypts or decrypts the data read from the stream in one go
// since this is what aesgcm.Seal/Open is made for
func (r *gcmcryptor) crypt() error {
	input := make([]byte, r.bc.reader.Size())
	_, err := r.bc.reader.ReadAt(input, 0)
	if err != nil {
		return errors.Wrap(err, "Could not read plain layer data")
	}

	block, err := aes.NewCipher(r.bc.key)
	if err != nil {
		return errors.Wrap(err, "Unable to AES generate block cipher")
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return errors.Wrap(err, "Unable to create new GCM object")
	}

	var output []byte
	if r.encrypt {
		output = aesgcm.Seal(nil, r.bc.nonce, input, nil)
	} else {
		output, err = aesgcm.Open(nil, r.bc.nonce, input, nil)
		if err != nil {
			return err
		}
	}
	r.outputReader = bytes.NewReader(output)
	r.size = int64(len(output))
	r.digester.Hash().Write(output)

	return nil
}

func (r *gcmcryptor) Read(p []byte) (int, error) {
	if r.outputReader == nil {
		if err := r.crypt(); err != nil {
			return 0, err
		}
	}

	return r.outputReader.Read(p)
}

// Size returns the Size of the encrypted data
func (r *gcmcryptor) Size() int64 {
	return r.size
}

func (r *gcmcryptor) Digest() digest.Digest {
	return r.digester.Digest()
}

func (r *gcmcryptor) Hash() hash.Hash {
	return r.digester.Hash()
}

// init initializes an instance
func (bc *GCMLayerBlockCipher) init(reader content.ReaderAt, opt LayerBlockCipherOptions) (LayerBlockCipherOptions, error) {
	var err error

	bc.reader = reader

	bc.key = opt.SymmetricKey
	if len(bc.key) != bc.bits/8 {
		return LayerBlockCipherOptions{}, errors.New("Invalid key length")
	}

	nonceStr := opt.CipherOptions["nonce"]
	if nonceStr != "" {
		// Decode nonce str
		bc.nonce, err = base64.StdEncoding.DecodeString(nonceStr)
		if err != nil {
			return LayerBlockCipherOptions{}, errors.New("Failed to decode nonce")
		}
	} else {
		// Never use more than 2^32 random nonces with a given key because of the risk of a repeat.
		bc.nonce = make([]byte, 12)
		if _, err := io.ReadFull(rand.Reader, bc.nonce); err != nil {
			return LayerBlockCipherOptions{}, errors.Wrap(err, "Unable to generate random nonce")
		}
	}

	lbco := LayerBlockCipherOptions{
		SymmetricKey: bc.key,
		CipherOptions: map[string]string{
			"nonce": base64.StdEncoding.EncodeToString(bc.nonce),
		},
	}

	return lbco, nil
}

// Encrypt takes in layer data and returns the ciphertext and relevant LayerBlockCipherOptions
func (bc *GCMLayerBlockCipher) Encrypt(plainDataReader content.ReaderAt, opt LayerBlockCipherOptions) (CryptedDataReader, LayerBlockCipherOptions, error) {
	lbco, err := bc.init(plainDataReader, opt)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, err
	}

	return &gcmcryptor{true, bc, nil, digest.SHA256.Digester(), 0}, lbco, nil
}

// Decrypt takes in layer ciphertext data and returns the plaintext and relevant LayerBlockCipherOptions
func (bc *GCMLayerBlockCipher) Decrypt(encDataReader content.ReaderAt, opt LayerBlockCipherOptions) (CryptedDataReader, LayerBlockCipherOptions, error) {
	lbco, err := bc.init(encDataReader, opt)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, err
	}

	return &gcmcryptor{false, bc, nil, digest.SHA256.Digester(), 0}, lbco, nil
}
