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
	_ "crypto/sha256"
	"testing"

	"github.com/containerd/containerd/content"
)

func TestBlockCipherAesGcmCreateValid(t *testing.T) {
	_, err := NewGCMLayerBlockCipher(128)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewGCMLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBlockCipherAesGcmCreateInvalid(t *testing.T) {
	_, err := NewGCMLayerBlockCipher(8)
	if err == nil {
		t.Fatal(err)
	}
	_, err = NewGCMLayerBlockCipher(255)
	if err == nil {
		t.Fatal(err)
	}
}

func TestBlockCipherAesGcmEncryption(t *testing.T) {
	var (
		symKey = []byte("01234567890123456789012345678912")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewGCMLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := content.BufReaderAt{int64(len(layerData)), layerData}
	ciphertextReader, lbco, err := bc.Encrypt(layerDataReader, opt)
	if err != nil {
		t.Fatal(err)
	}

	// Use a different instantiated object to indicate an invokation at a diff time
	bc2, err := NewGCMLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext := make([]byte, ciphertextReader.Size())
	ciphertextReader.Read(ciphertext)
	ciphertextReaderAt := content.BufReaderAt{int64(len(ciphertext)), ciphertext}

	plaintextReader, _, err := bc2.Decrypt(ciphertextReaderAt, lbco)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := make([]byte, plaintextReader.Size())
	plaintextReader.Read(plaintext)

	if string(plaintext) != string(layerData) {
		t.Fatal("Decrypted data is incorrect")
	}
}

func TestBlockCipherAesGcmEncryptionInvalidKey(t *testing.T) {
	var (
		symKey = []byte("01234567890123456789012345678912")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewGCMLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := content.BufReaderAt{int64(len(layerData)), layerData}
	ciphertextReader, lbco, err := bc.Encrypt(layerDataReader, opt)
	if err != nil {
		t.Fatal(err)
	}

	// Use a different instantiated object to indicate an invokation at a diff time
	bc2, err := NewGCMLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	lbco.SymmetricKey = []byte("aaa34567890123456789012345678912")

	ciphertext := make([]byte, ciphertextReader.Size())
	ciphertextReader.Read(ciphertext)
	ciphertextReaderAt := content.BufReaderAt{int64(len(ciphertext)), ciphertext}

	_, _, err = bc2.Decrypt(ciphertextReaderAt, lbco)
	if err == nil {
		t.Fatal(err)
	}
}

func TestBlockCipherAesGcmEncryptionInvalidKeyLength(t *testing.T) {
	var (
		symKey = []byte("012345")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewGCMLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := content.BufReaderAt{int64(len(layerData)), layerData}
	_, _, err = bc.Encrypt(layerDataReader, opt)
	if err == nil {
		t.Fatal(err)
	}
}
