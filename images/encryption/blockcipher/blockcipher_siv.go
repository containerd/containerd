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
	"crypto/rand"
	"fmt"
	"io"

	miscreant "github.com/miscreant/miscreant-go"
	"github.com/pkg/errors"
)

// AESSIVLayerBlockCipher implements the AES SIV block cipher
type AESSIVLayerBlockCipher struct {
	keylen    int // in bytes
	reader    io.Reader
	encryptor *miscreant.StreamEncryptor
	decryptor *miscreant.StreamDecryptor
	err       error  // error that occurred during operation
	eof       bool   // hit EOF in the input data
	toread    int    // how many bytes to read in one chunk
	inbuffer  []byte // input buffer with data from reader
	inoffset  int64  // offset where to read from next
	outbuffer []byte // output buffer to return to user
	outoffset int    // offset in output buffer
	outsize   int64  // output size
}

type aessivcryptor struct {
	bc           *AESSIVLayerBlockCipher
	outputReader io.Reader
}

// NewAESSIVLayerBlockCipher returns a new AES SIV block cipher of 256 or 512 bits
func NewAESSIVLayerBlockCipher(bits int) (LayerBlockCipher, error) {
	if bits != 256 && bits != 512 {
		return nil, errors.New("AES SIV bit count not supported")
	}
	return &AESSIVLayerBlockCipher{keylen: bits / 8}, nil
}

func (r *aessivcryptor) Read(p []byte) (int, error) {
	if r.bc.err != nil {
		return 0, r.bc.err
	}

	for {
		// return data if we have any
		if r.bc.outbuffer != nil && r.bc.outoffset < len(r.bc.outbuffer) {
			n := copy(p, r.bc.outbuffer[r.bc.outoffset:])
			r.bc.outoffset += n

			return n, nil
		}
		// no data and hit eof before?
		if r.bc.eof {
			return 0, io.EOF
		}
		// read new data; we expect to get r.bc.toread number of bytes
		// for anything less we assume it's EOF
		numbytes := 0
		for numbytes < r.bc.toread {
			var n int
			n, r.bc.err = r.bc.reader.Read(r.bc.inbuffer[numbytes:r.bc.toread])
			numbytes += n
			if r.bc.err != nil {
				if r.bc.err == io.EOF {
					r.bc.eof = true
					r.bc.err = nil
					break
				} else {
					return 0, r.bc.err
				}
			}
			if n == 0 {
				break
			}
		}
		if numbytes < r.bc.toread {
			r.bc.eof = true
		}

		r.bc.inoffset += int64(numbytes)

		// transform the data
		if r.bc.encryptor != nil {
			r.bc.outbuffer = r.bc.encryptor.Seal(nil, r.bc.inbuffer[:numbytes], []byte(""), r.bc.eof)
		} else {
			r.bc.outbuffer, r.bc.err = r.bc.decryptor.Open(nil, r.bc.inbuffer[:numbytes], []byte(""), r.bc.eof)
			if r.bc.err != nil {
				return 0, r.bc.err
			}
		}
		// let reader start from beginning of buffer
		r.bc.outoffset = 0
		r.bc.outsize += int64(len(r.bc.outbuffer))
	}
}

// init initializes an instance
func (bc *AESSIVLayerBlockCipher) init(encrypt bool, reader io.Reader, opt LayerBlockCipherOptions) (LayerBlockCipherOptions, error) {
	var (
		err error
		se  miscreant.StreamEncryptor
	)

	bc.reader = reader

	key := opt.SymmetricKey
	if len(key) != bc.keylen {
		return LayerBlockCipherOptions{}, fmt.Errorf("invalid key length of %d bytes; need %d bytes", len(key), bc.keylen)
	}

	nonce := opt.CipherOptions["nonce"]
	if len(nonce) == 0 {
		nonce = make([]byte, se.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return LayerBlockCipherOptions{}, errors.Wrap(err, "unable to generate random nonce")
		}
	}

	bc.inbuffer = make([]byte, 1024*1024)
	bc.toread = len(bc.inbuffer)
	bc.inoffset = 0
	bc.outbuffer = nil
	bc.outoffset = 0
	bc.eof = false
	bc.err = nil
	bc.outsize = 0

	if encrypt {
		bc.encryptor, err = miscreant.NewStreamEncryptor("AES-SIV", key, nonce)
		if err != nil {
			return LayerBlockCipherOptions{}, errors.Wrap(err, "unable to create AES-SIV stream encryptor")
		}
		bc.toread -= bc.encryptor.Overhead()
		bc.decryptor = nil
	} else {
		bc.decryptor, err = miscreant.NewStreamDecryptor("AES-SIV", key, nonce)
		if err != nil {
			return LayerBlockCipherOptions{}, errors.Wrap(err, "unable to create AES-SIV stream decryptor")
		}
		bc.encryptor = nil
	}

	lbco := LayerBlockCipherOptions{
		SymmetricKey: key,
		CipherOptions: map[string][]byte{
			"nonce": nonce,
		},
	}

	return lbco, nil
}

// GenerateKey creates a synmmetric key
func (bc *AESSIVLayerBlockCipher) GenerateKey() []byte {
	return miscreant.GenerateKey(bc.keylen)
}

// Encrypt takes in layer data and returns the ciphertext and relevant LayerBlockCipherOptions
func (bc *AESSIVLayerBlockCipher) Encrypt(plainDataReader io.Reader, opt LayerBlockCipherOptions) (io.Reader, LayerBlockCipherOptions, error) {
	lbco, err := bc.init(true, plainDataReader, opt)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, err
	}

	return &aessivcryptor{bc, nil}, lbco, nil
}

// Decrypt takes in layer ciphertext data and returns the plaintext and relevant LayerBlockCipherOptions
func (bc *AESSIVLayerBlockCipher) Decrypt(encDataReader io.Reader, opt LayerBlockCipherOptions) (io.Reader, LayerBlockCipherOptions, error) {
	lbco, err := bc.init(false, encDataReader, opt)
	if err != nil {
		return nil, LayerBlockCipherOptions{}, err
	}

	return &aessivcryptor{bc, nil}, lbco, nil
}
