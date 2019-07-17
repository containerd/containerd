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
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/pkg/encryption/blockcipher"
	"github.com/containerd/containerd/pkg/encryption/config"
	"github.com/containerd/containerd/pkg/encryption/keywrap"
	"github.com/containerd/containerd/pkg/encryption/keywrap/jwe"
	"github.com/containerd/containerd/pkg/encryption/keywrap/pgp"
	"github.com/containerd/containerd/pkg/encryption/keywrap/pkcs7"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

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
func EncryptLayer(ec *config.EncryptConfig, encOrPlainLayerReader io.Reader, desc ocispec.Descriptor) (io.Reader, map[string]string, error) {
	var (
		encLayerReader io.Reader
		err            error
		optsData       []byte
	)

	if ec == nil {
		return nil, nil, errors.Wrapf(errdefs.ErrInvalidArgument, "EncryptConfig must not be nil")
	}

	for annotationsID := range keyWrapperAnnotations {
		annotation := desc.Annotations[annotationsID]
		if annotation != "" {
			optsData, err = decryptLayerKeyOptsData(&ec.DecryptConfig, desc)
			if err != nil {
				return nil, nil, err
			}
			// already encrypted!
		}
	}

	newAnnotations := make(map[string]string)

	for annotationsID, scheme := range keyWrapperAnnotations {
		b64Annotations := desc.Annotations[annotationsID]
		if b64Annotations == "" && optsData == nil {
			encLayerReader, optsData, err = commonEncryptLayer(encOrPlainLayerReader, desc.Digest, blockcipher.AESSIVCMAC512)
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
		err = errors.Errorf("no encryptor found to handle encryption")
	}
	// if nothing was encrypted, we just return encLayer = nil
	return encLayerReader, newAnnotations, err
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
// If unwrapOnly is set we will only try to decrypt the layer encryption key and return
func DecryptLayer(dc *config.DecryptConfig, encLayerReader io.Reader, desc ocispec.Descriptor, unwrapOnly bool) (io.Reader, digest.Digest, error) {
	if dc == nil {
		return nil, "", errors.Wrapf(errdefs.ErrInvalidArgument, "DecryptConfig must not be nil")
	}
	optsData, err := decryptLayerKeyOptsData(dc, desc)
	if err != nil || unwrapOnly {
		return nil, "", err
	}

	return commonDecryptLayer(encLayerReader, optsData)
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
		return nil, errors.New("missing private key needed for decryption")
	}
	return nil, errors.Errorf("no suitable key unwrapper found or none of the private keys could be used for decryption")
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
			return nil, errors.New("could not base64 decode the annotation")
		}
		optsData, err := keywrapper.UnwrapKey(dc, annotation)
		if err != nil {
			continue
		}
		return optsData, nil
	}
	return nil, errors.New("no suitable key found for decrypting layer key")
}

// commonEncryptLayer is a function to encrypt the plain layer using a new random
// symmetric key and return the LayerBlockCipherHandler's JSON in string form for
// later use during decryption
func commonEncryptLayer(plainLayerReader io.Reader, d digest.Digest, typ blockcipher.LayerCipherType) (io.Reader, []byte, error) {
	lbch, err := blockcipher.NewLayerBlockCipherHandler()
	if err != nil {
		return nil, nil, err
	}

	encLayerReader, opts, err := lbch.Encrypt(plainLayerReader, typ)
	if err != nil {
		return nil, nil, err
	}
	opts.Digest = d

	optsData, err := json.Marshal(opts)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "could not JSON marshal opts")
	}

	return encLayerReader, optsData, err
}

// commonDecryptLayer decrypts an encrypted layer previously encrypted with commonEncryptLayer
// by passing along the optsData
func commonDecryptLayer(encLayerReader io.Reader, optsData []byte) (io.Reader, digest.Digest, error) {
	opts := blockcipher.LayerBlockCipherOptions{}
	err := json.Unmarshal(optsData, &opts)
	if err != nil {
		return nil, "", errors.Wrapf(err, "could not JSON unmarshal optsData")
	}

	lbch, err := blockcipher.NewLayerBlockCipherHandler()
	if err != nil {
		return nil, "", err
	}

	plainLayerReader, opts, err := lbch.Decrypt(encLayerReader, opts)
	if err != nil {
		return nil, "", err
	}

	return plainLayerReader, opts.Digest, nil
}
