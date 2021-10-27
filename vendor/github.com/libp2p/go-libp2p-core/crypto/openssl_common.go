// +build openssl

package crypto

import (
	"sync"

	pb "github.com/libp2p/go-libp2p-core/crypto/pb"

	openssl "github.com/libp2p/go-openssl"
)

// define these as separate types so we can add more key types later and reuse
// code.

type opensslPublicKey struct {
	key openssl.PublicKey

	cacheLk sync.Mutex
	cached  []byte
}

type opensslPrivateKey struct {
	key openssl.PrivateKey
}

func unmarshalOpensslPrivateKey(b []byte) (opensslPrivateKey, error) {
	sk, err := openssl.LoadPrivateKeyFromDER(b)
	if err != nil {
		return opensslPrivateKey{}, err
	}
	return opensslPrivateKey{sk}, nil
}

func unmarshalOpensslPublicKey(b []byte) (opensslPublicKey, error) {
	sk, err := openssl.LoadPublicKeyFromDER(b)
	if err != nil {
		return opensslPublicKey{}, err
	}
	return opensslPublicKey{key: sk, cached: b}, nil
}

// Verify compares a signature against input data
func (pk *opensslPublicKey) Verify(data, sig []byte) (bool, error) {
	err := pk.key.VerifyPKCS1v15(openssl.SHA256_Method, data, sig)
	return err == nil, err
}

func (pk *opensslPublicKey) Type() pb.KeyType {
	switch pk.key.KeyType() {
	case openssl.KeyTypeRSA:
		return pb.KeyType_RSA
	default:
		return -1
	}
}

// Bytes returns protobuf bytes of a public key
func (pk *opensslPublicKey) Bytes() ([]byte, error) {
	pk.cacheLk.Lock()
	var err error
	if pk.cached == nil {
		pk.cached, err = MarshalPublicKey(pk)
	}
	pk.cacheLk.Unlock()
	return pk.cached, err
}

func (pk *opensslPublicKey) Raw() ([]byte, error) {
	return pk.key.MarshalPKIXPublicKeyDER()
}

// Equals checks whether this key is equal to another
func (pk *opensslPublicKey) Equals(k Key) bool {
	k0, ok := k.(*RsaPublicKey)
	if !ok {
		return basicEquals(pk, k)
	}

	return pk.key.Equal(k0.opensslPublicKey.key)
}

// Sign returns a signature of the input data
func (sk *opensslPrivateKey) Sign(message []byte) ([]byte, error) {
	return sk.key.SignPKCS1v15(openssl.SHA256_Method, message)
}

// GetPublic returns a public key
func (sk *opensslPrivateKey) GetPublic() PubKey {
	return &opensslPublicKey{key: sk.key}
}

func (sk *opensslPrivateKey) Type() pb.KeyType {
	switch sk.key.KeyType() {
	case openssl.KeyTypeRSA:
		return pb.KeyType_RSA
	default:
		return -1
	}
}

// Bytes returns protobuf bytes from a private key
func (sk *opensslPrivateKey) Bytes() ([]byte, error) {
	return MarshalPrivateKey(sk)
}

func (sk *opensslPrivateKey) Raw() ([]byte, error) {
	return sk.key.MarshalPKCS1PrivateKeyDER()
}

// Equals checks whether this key is equal to another
func (sk *opensslPrivateKey) Equals(k Key) bool {
	k0, ok := k.(*RsaPrivateKey)
	if !ok {
		return basicEquals(sk, k)
	}

	return sk.key.Equal(k0.opensslPrivateKey.key)
}
