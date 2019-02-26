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

	"github.com/containerd/containerd/content"

	"github.com/pkg/errors"
)

// CryptedDataReader wraps the io.Reader and adds the Size of the encrypted or decrypted
// data to it as well as its hash
type CryptedDataReader interface {
	content.ReaderDigester
}

// LayerCipherType is the ciphertype as specified in the layer metadata
type LayerCipherType string

// TODO: Should be obtained from OCI spec once included
const (
	AESSIVCMAC256 LayerCipherType = "AES-SIV-CMAC-256"
	AESSIVCMAC512 LayerCipherType = "AES-SIV-CMAC-512"
	CipherTypeOpt string          = "type"
)

// LayerBlockCipherOptions includes the information required to encrypt/decrypt
// an image
type LayerBlockCipherOptions struct {
	SymmetricKey  []byte            `json:'symkey'`
	CipherOptions map[string]string `json:'cipheroptions'`
}

// LayerBlockCipher returns a provider for encrypt/decrypt functionality
// for handling the layer data for a specific algorithm
type LayerBlockCipher interface {
	// GenerateKey creates a symmetric key
	GenerateKey() []byte
	// Encrypt takes in layer data and returns the ciphertext and relevant LayerBlockCipherOptions
	Encrypt(layerDataReader io.ReaderAt, opt LayerBlockCipherOptions) (CryptedDataReader, LayerBlockCipherOptions, error)
	// Decrypt takes in layer ciphertext data and returns the plaintext and relevant LayerBlockCipherOptions
	Decrypt(layerDataReader io.ReaderAt, opt LayerBlockCipherOptions) (CryptedDataReader, LayerBlockCipherOptions, error)
}

// LayerBlockCipherHandler is the handler for encrypt/decrypt for layers
type LayerBlockCipherHandler struct {
	cipherMap map[LayerCipherType]LayerBlockCipher
}

// Encrypt is the handler for the layer decryption routine
func (h *LayerBlockCipherHandler) Encrypt(plainDataReader io.ReaderAt, typ LayerCipherType) (CryptedDataReader, LayerBlockCipherOptions, error) {

	if c, ok := h.cipherMap[typ]; ok {
		opt := LayerBlockCipherOptions{
			SymmetricKey: c.GenerateKey(),
		}
		encDataReader, newopt, err := c.Encrypt(plainDataReader, opt)
		if err == nil {
			newopt.CipherOptions[CipherTypeOpt] = string(typ)
		}
		return encDataReader, newopt, err
	}
	return nil, LayerBlockCipherOptions{}, errors.New("Not supported Cipher Type")
}

// Decrypt is the handler for the layer decryption routine
func (h *LayerBlockCipherHandler) Decrypt(encDataReader io.ReaderAt, opt LayerBlockCipherOptions) (CryptedDataReader, LayerBlockCipherOptions, error) {
	typ, ok := opt.CipherOptions[CipherTypeOpt]
	if !ok {
		return nil, LayerBlockCipherOptions{}, errors.New("No cipher type provided")
	}
	if c, ok := h.cipherMap[LayerCipherType(typ)]; ok {
		return c.Decrypt(encDataReader, opt)
	}
	return nil, LayerBlockCipherOptions{}, errors.New("Not supported Cipher Type")
}

// NewLayerBlockCipherHandler returns a new default handler
func NewLayerBlockCipherHandler() (*LayerBlockCipherHandler, error) {
	h := LayerBlockCipherHandler{
		cipherMap: map[LayerCipherType]LayerBlockCipher{},
	}

	var err error
	h.cipherMap[AESSIVCMAC256], err = NewAESSIVLayerBlockCipher(256)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to set up Cipher AES-SIV-CMAC-256")
	}

	h.cipherMap[AESSIVCMAC512], err = NewAESSIVLayerBlockCipher(512)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to set up Cipher AES-SIV-CMAC-512")
	}

	return &h, nil
}
