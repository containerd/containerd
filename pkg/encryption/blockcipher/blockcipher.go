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
	"io"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

// LayerCipherType is the ciphertype as specified in the layer metadata
type LayerCipherType string

// TODO: Should be obtained from OCI spec once included
const (
	AESSIVCMAC256 LayerCipherType = "AEAD_AES_SIV_CMAC_STREAM_256"
	AESSIVCMAC512 LayerCipherType = "AEAD_AES_SIV_CMAC_STREAM_512"
	CipherTypeOpt string          = "type"
)

// LayerBlockCipherOptions includes the information required to encrypt/decrypt
// an image
type LayerBlockCipherOptions struct {
	// SymmetricKey represents the symmetric key used for encryption/decryption
	// This field should be populated by Encrypt/Decrypt calls
	SymmetricKey []byte `json:"symkey"`

	// Digest is the digest of the original data for verification.
	// This is NOT populated by Encrypt/Decrypt calls
	Digest digest.Digest `json:"digest"`

	// CipherOptions contains the cipher metadata used for encryption/decryption
	// This field should be populated by Encrypt/Decrypt calls
	CipherOptions map[string][]byte `json:"cipheroptions"`
}

// LayerBlockCipher returns a provider for encrypt/decrypt functionality
// for handling the layer data for a specific algorithm
type LayerBlockCipher interface {
	// GenerateKey creates a symmetric key
	GenerateKey() []byte
	// Encrypt takes in layer data and returns the ciphertext and relevant LayerBlockCipherOptions
	Encrypt(layerDataReader io.Reader, opt LayerBlockCipherOptions) (io.Reader, LayerBlockCipherOptions, error)
	// Decrypt takes in layer ciphertext data and returns the plaintext and relevant LayerBlockCipherOptions
	Decrypt(layerDataReader io.Reader, opt LayerBlockCipherOptions) (io.Reader, LayerBlockCipherOptions, error)
}

// LayerBlockCipherHandler is the handler for encrypt/decrypt for layers
type LayerBlockCipherHandler struct {
	cipherMap map[LayerCipherType]LayerBlockCipher
}

// Encrypt is the handler for the layer decryption routine
func (h *LayerBlockCipherHandler) Encrypt(plainDataReader io.Reader, typ LayerCipherType) (io.Reader, LayerBlockCipherOptions, error) {

	if c, ok := h.cipherMap[typ]; ok {
		opt := LayerBlockCipherOptions{
			SymmetricKey: c.GenerateKey(),
		}
		encDataReader, newopt, err := c.Encrypt(plainDataReader, opt)
		if err == nil {
			newopt.CipherOptions[CipherTypeOpt] = []byte(typ)
		}
		return encDataReader, newopt, err
	}
	return nil, LayerBlockCipherOptions{}, errors.Errorf("unsupported cipher type: %s", typ)
}

// Decrypt is the handler for the layer decryption routine
func (h *LayerBlockCipherHandler) Decrypt(encDataReader io.Reader, opt LayerBlockCipherOptions) (io.Reader, LayerBlockCipherOptions, error) {
	typ, ok := opt.CipherOptions[CipherTypeOpt]
	if !ok {
		return nil, LayerBlockCipherOptions{}, errors.New("no cipher type provided")
	}
	if c, ok := h.cipherMap[LayerCipherType(typ)]; ok {
		return c.Decrypt(encDataReader, opt)
	}
	return nil, LayerBlockCipherOptions{}, errors.Errorf("unsupported cipher type: %s", typ)
}

// NewLayerBlockCipherHandler returns a new default handler
func NewLayerBlockCipherHandler() (*LayerBlockCipherHandler, error) {
	h := LayerBlockCipherHandler{
		cipherMap: map[LayerCipherType]LayerBlockCipher{},
	}

	var err error
	h.cipherMap[AESSIVCMAC256], err = NewAESSIVLayerBlockCipher(256)
	if err != nil {
		return nil, errors.Wrap(err, "unable to set up Cipher AES-SIV-CMAC-256")
	}

	h.cipherMap[AESSIVCMAC512], err = NewAESSIVLayerBlockCipher(512)
	if err != nil {
		return nil, errors.Wrap(err, "unable to set up Cipher AES-SIV-CMAC-512")
	}

	return &h, nil
}
