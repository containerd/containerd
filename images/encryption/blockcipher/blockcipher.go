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
	"github.com/pkg/errors"
)

// LayerCipherType is the ciphertype as specified in the layer metadata
type LayerCipherType string

// TODO: Should be obtained from OCI spec once included
const (
	AEADAES128GCM LayerCipherType = "AEAD_AES_128_GCM"
	AEADAES256GCM LayerCipherType = "AEAD_AES_256_GCM"
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
	// Encrypt takes in layer data and returns the ciphertext and relevant LayerBlockCipherOptions
	Encrypt(layerData []byte, opt LayerBlockCipherOptions) ([]byte, LayerBlockCipherOptions, error)
	// Decrypt takes in layer ciphertext data and returns the plaintext and relevant LayerBlockCipherOptions
	Decrypt(layerData []byte, opt LayerBlockCipherOptions) ([]byte, LayerBlockCipherOptions, error)
}

// LayerBlockCipherHandler is the handler for encrypt/decrypt for layers
type LayerBlockCipherHandler struct {
	cipherMap map[LayerCipherType]LayerBlockCipher
}

// Encrypt is the handler for the layer decrpytion routine
func (h *LayerBlockCipherHandler) Encrypt(layerData []byte, typ LayerCipherType, opt LayerBlockCipherOptions) ([]byte, LayerBlockCipherOptions, error) {
	if c, ok := h.cipherMap[typ]; ok {
		data, newopt, err := c.Encrypt(layerData, opt)
		if err == nil {
			newopt.CipherOptions[CipherTypeOpt] = string(typ)
		}
		return data, newopt, err
	}
	return nil, LayerBlockCipherOptions{}, errors.New("Not supported Cipher Type")
}

// Decrypt is the handler for the layer decrpytion routine
func (h *LayerBlockCipherHandler) Decrypt(layerData []byte, opt LayerBlockCipherOptions) ([]byte, LayerBlockCipherOptions, error) {
	typ, ok := opt.CipherOptions[CipherTypeOpt]
	if !ok {
		return nil, LayerBlockCipherOptions{}, errors.New("No cipher type provided")
	}
	if c, ok := h.cipherMap[LayerCipherType(typ)]; ok {
		return c.Decrypt(layerData, opt)
	}
	return nil, LayerBlockCipherOptions{}, errors.New("Not supported Cipher Type")
}

// NewLayerBlockCipherHandler returns a new default handler
func NewLayerBlockCipherHandler() (*LayerBlockCipherHandler, error) {
	h := LayerBlockCipherHandler{
		cipherMap: map[LayerCipherType]LayerBlockCipher{},
	}

	var err error
	h.cipherMap[AEADAES128GCM], err = NewGCMLayerBlockCipher(128)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to set up Cipher GCM 128")
	}

	h.cipherMap[AEADAES256GCM], err = NewGCMLayerBlockCipher(256)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to set up Cipher GCM 256")
	}

	return &h, nil
}
