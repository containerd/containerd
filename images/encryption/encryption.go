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

package encryption

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images/encryption/blockcipher"
	"github.com/containerd/containerd/images/encryption/config"
	"github.com/containerd/containerd/images/encryption/keywrap"

	"github.com/containerd/containerd/images/encryption/keywrap/jwe"
	"github.com/containerd/containerd/images/encryption/keywrap/pgp"
	"github.com/containerd/containerd/images/encryption/keywrap/pkcs7"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// LayerInfo holds information about an image layer
type LayerInfo struct {
	// The Number of this layer in the sequence; starting at 0
	Index      uint32
	Descriptor ocispec.Descriptor
}

// LayerFilter holds criteria for which layer to select
type LayerFilter struct {
	// IDs of layers to touch; may be negative number to start from topmost layer
	// empty array means 'all layers'
	Layers []int32
	// Platforms to touch; empty array means 'all platforms'
	Platforms []ocispec.Platform
}

func init() {
	keyWrappers = make(map[string]keywrap.KeyWrapper)
	keyWrapperAnnotations = make(map[string]string)
	RegisterKeyWrapper("pgp", pgp.NewKeyWrapper())
	RegisterKeyWrapper("jwe", jwe.NewKeyWrapper())
	RegisterKeyWrapper("pkcs7", pkcs7.NewKeyWrapper())
}

var keyWrappers map[string]keywrap.KeyWrapper
var keyWrapperAnnotations map[string]string

// RegisterKeyWrapper allows to register key wrappers by their encryption scheme
func RegisterKeyWrapper(scheme string, iface keywrap.KeyWrapper) {
	keyWrappers[scheme] = iface
	keyWrapperAnnotations[iface.GetAnnotationID()] = scheme
}

// GetKeyWrapper looks up the encryptor interface given an encryption scheme (gpg, jwe)
func GetKeyWrapper(scheme string) keywrap.KeyWrapper {
	return keyWrappers[scheme]
}

// GetWrappedKeysMap returns a map of wrappedKeys as values in a
// map with the encryption scheme(s) as the key(s)
func GetWrappedKeysMap(desc ocispec.Descriptor) map[string]string {
	wrappedKeysMap := make(map[string]string)

	for annotationsID, scheme := range keyWrapperAnnotations {
		if annotation, ok := desc.Annotations[annotationsID]; ok {
			wrappedKeysMap[scheme] = annotation
		}
	}
	return wrappedKeysMap
}

// EncryptLayer encrypts the layer by running one encryptor after the other
func EncryptLayer(ec *config.EncryptConfig, encOrPlainLayer []byte, desc ocispec.Descriptor) ([]byte, map[string]string, error) {
	var (
		encLayer []byte
		err      error
		optsData []byte
	)

	if ec == nil {
		return nil, nil, errors.Wrapf(errdefs.ErrInvalidArgument, "EncryptConfig must not be nil")
	}

	symKey := make([]byte, 256/8)
	_, err = io.ReadFull(rand.Reader, symKey)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Could not create symmetric key")
	}

	for annotationsID := range keyWrapperAnnotations {
		annotation := desc.Annotations[annotationsID]
		if annotation != "" {
			optsData, err = decryptLayerKeyOptsData(&ec.Dc, desc)
			if err != nil {
				return nil, nil, err
			}
			// already encrypted!
			encLayer = encOrPlainLayer
		}
	}

	newAnnotations := make(map[string]string)

	for annotationsID, scheme := range keyWrapperAnnotations {
		b64Annotations := desc.Annotations[annotationsID]
		if b64Annotations == "" && optsData == nil {
			encLayer, optsData, err = commonEncryptLayer(encOrPlainLayer, symKey, blockcipher.AEADAES256GCM)
			if err != nil {
				return nil, nil, err
			}
		}
		keywrapper := GetKeyWrapper(scheme)
		b64Annotations, err = preWrapKeys(keywrapper, ec, b64Annotations, optsData)
		if err != nil {
			return nil, nil, err
		}
		if b64Annotations != "" {
			newAnnotations[annotationsID] = b64Annotations
		}
	}
	if len(newAnnotations) == 0 {
		err = errors.Errorf("No encryptor found to handle encryption")
	}
	// if nothing was encrypted, we just return encLayer = nil
	return encLayer, newAnnotations, err
}

// preWrapKeys calls WrapKeys and handles the base64 encoding and concatenation of the
// annotation data
func preWrapKeys(keywrapper keywrap.KeyWrapper, ec *config.EncryptConfig, b64Annotations string, optsData []byte) (string, error) {
	newAnnotation, err := keywrapper.WrapKeys(ec, optsData)
	if err != nil || len(newAnnotation) == 0 {
		return b64Annotations, err
	}
	b64newAnnotation := base64.StdEncoding.EncodeToString(newAnnotation)
	if b64Annotations == "" {
		return b64newAnnotation, nil
	}
	return b64Annotations + "," + b64newAnnotation, nil
}

// DecryptLayer decrypts a layer trying one keywrap.KeyWrapper after the other to see whether it
// can apply the provided private key
func DecryptLayer(dc *config.DecryptConfig, encLayer []byte, desc ocispec.Descriptor) ([]byte, error) {
	if dc == nil {
		return nil, errors.Wrapf(errdefs.ErrInvalidArgument, "DecryptConfig must not be nil")
	}
	optsData, err := decryptLayerKeyOptsData(dc, desc)
	if err != nil {
		return nil, err
	}

	return commonDecryptLayer(encLayer, optsData)
}

func decryptLayerKeyOptsData(dc *config.DecryptConfig, desc ocispec.Descriptor) ([]byte, error) {
	privKeyGiven := false
	for annotationsID, scheme := range keyWrapperAnnotations {
		b64Annotation := desc.Annotations[annotationsID]
		if b64Annotation != "" {
			keywrapper := GetKeyWrapper(scheme)

			if len(keywrapper.GetPrivateKeys(dc.Parameters)) == 0 {
				continue
			}
			privKeyGiven = true

			optsData, err := preUnwrapKey(keywrapper, dc, b64Annotation)
			if err != nil {
				// try next keywrap.KeyWrapper
				continue
			}
			if optsData == nil {
				// try next keywrap.KeyWrapper
				continue
			}
			return optsData, nil
		}
	}
	if !privKeyGiven {
		return nil, errors.New("Missing private key needed for decryption")
	}
	return nil, errors.Errorf("No suitable key unwrapper found or none of the private keys could be used for decryption")
}

// preUnwrapKey decodes the comma separated base64 strings and calls the Unwrap function
// of the given keywrapper with it and returns the result in case the Unwrap functions
// does not return an error. If all attempts fail, an error is returned.
func preUnwrapKey(keywrapper keywrap.KeyWrapper, dc *config.DecryptConfig, b64Annotations string) ([]byte, error) {
	if b64Annotations == "" {
		return nil, nil
	}
	for _, b64Annotation := range strings.Split(b64Annotations, ",") {
		annotation, err := base64.StdEncoding.DecodeString(b64Annotation)
		if err != nil {
			return nil, errors.New("Could not base64 decode the annotation")
		}
		optsData, err := keywrapper.UnwrapKey(dc, annotation)
		if err != nil {
			continue
		}
		return optsData, nil
	}
	return nil, errors.New("No suitable key found for decrypting layer key")
}

// commonEncryptLayer is a function to encrypt the plain layer using a new random
// symmetric key and return the LayerBlockCipherHandler's JSON in string form for
// later use during decryption
func commonEncryptLayer(plainLayer []byte, symKey []byte, typ blockcipher.LayerCipherType) ([]byte, []byte, error) {
	opts := blockcipher.LayerBlockCipherOptions{
		SymmetricKey: symKey,
	}
	lbch, err := blockcipher.NewLayerBlockCipherHandler()
	if err != nil {
		return nil, nil, err
	}

	encLayer, opts, err := lbch.Encrypt(plainLayer, typ, opts)
	if err != nil {
		return nil, nil, err
	}

	optsData, err := json.Marshal(opts)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Could not JSON marshal opts")
	}
	return encLayer, optsData, err
}

// commonDecryptLayer decrypts an encrypted layer previously encrypted with commonEncryptLayer
// by passing along the optsData
func commonDecryptLayer(encLayer []byte, optsData []byte) ([]byte, error) {
	opts := blockcipher.LayerBlockCipherOptions{}
	err := json.Unmarshal(optsData, &opts)
	if err != nil {
		return nil, errors.Wrapf(err, "Could not JSON unmarshal optsData")
	}

	lbch, err := blockcipher.NewLayerBlockCipherHandler()
	if err != nil {
		return nil, err
	}

	plainLayer, opts, err := lbch.Decrypt(encLayer, opts)
	if err != nil {
		return nil, err
	}

	return plainLayer, nil
}
