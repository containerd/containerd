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
	_ "crypto/sha256"
	"io"
	"testing"
)

func TestBlockCipherAesSivCreateValid(t *testing.T) {
	_, err := NewAESSIVLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewAESSIVLayerBlockCipher(512)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBlockCipherAesSivCreateInvalid(t *testing.T) {
	_, err := NewAESSIVLayerBlockCipher(8)
	if err == nil {
		t.Fatal("Test should have failed due to invalid cipher size")
	}
	_, err = NewAESSIVLayerBlockCipher(255)
	if err == nil {
		t.Fatal("Test should have failed due to invalid cipher size")
	}
}

func TestBlockCipherAesSivEncryption(t *testing.T) {
	var (
		symKey = []byte("01234567890123456789012345678912")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewAESSIVLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := bytes.NewReader(layerData)
	ciphertextReader, lbco, err := bc.Encrypt(layerDataReader, opt)
	if err != nil {
		t.Fatal(err)
	}

	// Use a different instantiated object to indicate an invocation at a diff time
	bc2, err := NewAESSIVLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext := make([]byte, 1024)
	ciphertextReader.Read(ciphertext)
	ciphertextReaderAt := bytes.NewReader(ciphertext[:ciphertextReader.Size()])

	plaintextReader, _, err := bc2.Decrypt(ciphertextReaderAt, lbco)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := make([]byte, 1024)
	_, err = plaintextReader.Read(plaintext)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}

	if string(plaintext[:plaintextReader.Size()]) != string(layerData) {
		t.Fatal("Decrypted data is incorrect")
	}
}

func TestBlockCipherAesSivEncryptionInvalidKey(t *testing.T) {
	var (
		symKey = []byte("01234567890123456789012345678912")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewAESSIVLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := bytes.NewReader(layerData)
	ciphertextReader, lbco, err := bc.Encrypt(layerDataReader, opt)
	if err != nil {
		t.Fatal(err)
	}

	// Use a different instantiated object to indicate an invokation at a diff time
	bc2, err := NewAESSIVLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	lbco.SymmetricKey = []byte("aaa34567890123456789012345678912")

	ciphertext := make([]byte, 1024)
	ciphertextReader.Read(ciphertext)
	ciphertextReaderAt := bytes.NewReader(ciphertext[:ciphertextReader.Size()])

	plaintextReader, _, err := bc2.Decrypt(ciphertextReaderAt, lbco)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := make([]byte, 1024)
	_, err = plaintextReader.Read(plaintext)
	if err == nil {
		t.Fatal("Read() should have failed due to wrong key")
	}
}

func TestBlockCipherAesSivEncryptionInvalidKeyLength(t *testing.T) {
	var (
		symKey = []byte("012345")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewAESSIVLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := bytes.NewReader(layerData)
	_, _, err = bc.Encrypt(layerDataReader, opt)
	if err == nil {
		t.Fatal("Test should have failed due to invalid key length")
	}
}
