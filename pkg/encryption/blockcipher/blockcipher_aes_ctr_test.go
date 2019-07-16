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

func TestBlockCipherAesCtrCreateValid(t *testing.T) {
	_, err := NewAESCTRLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}
}

func TestBlockCipherAesCtrCreateInvalid(t *testing.T) {
	_, err := NewAESCTRLayerBlockCipher(8)
	if err == nil {
		t.Fatal("Test should have failed due to invalid cipher size")
	}
	_, err = NewAESCTRLayerBlockCipher(255)
	if err == nil {
		t.Fatal("Test should have failed due to invalid cipher size")
	}
}

func TestBlockCipherAesCtrEncryption(t *testing.T) {
	var (
		symKey = []byte("01234567890123456789012345678912")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
		myhmac    []byte
	)

	bc, err := NewAESCTRLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := bytes.NewReader(layerData)
	setHmac := func(hmac []byte) {
		myhmac = hmac
	}
	ciphertextReader, lbco, err := bc.Encrypt(layerDataReader, opt, setHmac)
	if err != nil {
		t.Fatal(err)
	}

	// Use a different instantiated object to indicate an invocation at a diff time
	bc2, err := NewAESCTRLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	ciphertext := make([]byte, 1024)
	encsize, err := ciphertextReader.Read(ciphertext)
	if err != io.EOF {
		t.Fatal("Expected EOF")
	}
	// HMAC must be available after Read() of encrypted data
	if len(myhmac) == 0 {
		t.Fatal("HMAC has not been calculated")
	}

	ciphertextTestReader := bytes.NewReader(ciphertext[:encsize])

	plaintextReader, _, err := bc2.Decrypt(ciphertextTestReader, lbco, myhmac)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := make([]byte, 1024)
	size, err := plaintextReader.Read(plaintext)
	if err != io.EOF {
		t.Fatal("Expected EOF")
	}

	if string(plaintext[:size]) != string(layerData) {
		t.Fatalf("expected %q, got %q", layerData, plaintext[:size])
	}
}

func TestBlockCipherAesCtrEncryptionInvalidKey(t *testing.T) {
	var (
		symKey = []byte("01234567890123456789012345678912")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
		myhmac    []byte
	)

	bc, err := NewAESCTRLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := bytes.NewReader(layerData)
	setHmac := func(hmac []byte) {
		myhmac = hmac
	}
	ciphertextReader, lbco, err := bc.Encrypt(layerDataReader, opt, setHmac)
	if err != nil {
		t.Fatal(err)
	}

	// Use a different instantiated object to indicate an invokation at a diff time
	bc2, err := NewAESCTRLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	lbco.SymmetricKey = []byte("aaa34567890123456789012345678912")

	ciphertext := make([]byte, 1024)
	encsize, err := ciphertextReader.Read(ciphertext)
	if err != io.EOF {
		t.Fatal("Expected EOF")
	}
	// HMAC must be available after Read() to get encrypted data
	if len(myhmac) == 0 {
		t.Fatal("HMAC has not been calculated")
	}
	ciphertextTestReader := bytes.NewReader(ciphertext[:encsize])

	plaintextReader, _, err := bc2.Decrypt(ciphertextTestReader, lbco, myhmac)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := make([]byte, 1024)
	// first time read may not hit EOF of original source
	_, _ = plaintextReader.Read(plaintext)
	// now we must have hit eof and evaluated the plaintext
	_, err = plaintextReader.Read(plaintext)
	if err == nil {
		t.Fatal("Read() should have failed due to wrong key")
	}
}

func TestBlockCipherAesCtrEncryptionInvalidKeyLength(t *testing.T) {
	var (
		symKey = []byte("012345")
		opt    = LayerBlockCipherOptions{
			SymmetricKey: symKey,
		}
		layerData = []byte("this is some data")
	)

	bc, err := NewAESCTRLayerBlockCipher(256)
	if err != nil {
		t.Fatal(err)
	}

	layerDataReader := bytes.NewReader(layerData)
	setHmac := func(hmac []byte) {}
	_, _, err = bc.Encrypt(layerDataReader, opt, setHmac)
	if err == nil {
		t.Fatal("Test should have failed due to invalid key length")
	}
}
