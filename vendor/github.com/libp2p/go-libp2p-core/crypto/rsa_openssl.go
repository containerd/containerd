// +build openssl

package crypto

import (
	"errors"
	"io"

	openssl "github.com/libp2p/go-openssl"
)

// RsaPrivateKey is an rsa private key
type RsaPrivateKey struct {
	opensslPrivateKey
}

// RsaPublicKey is an rsa public key
type RsaPublicKey struct {
	opensslPublicKey
}

// GenerateRSAKeyPair generates a new rsa private and public key
func GenerateRSAKeyPair(bits int, _ io.Reader) (PrivKey, PubKey, error) {
	if bits < MinRsaKeyBits {
		return nil, nil, ErrRsaKeyTooSmall
	}

	key, err := openssl.GenerateRSAKey(bits)
	if err != nil {
		return nil, nil, err
	}
	return &RsaPrivateKey{opensslPrivateKey{key}}, &RsaPublicKey{opensslPublicKey{key: key}}, nil
}

// GetPublic returns a public key
func (sk *RsaPrivateKey) GetPublic() PubKey {
	return &RsaPublicKey{opensslPublicKey{key: sk.opensslPrivateKey.key}}
}

// UnmarshalRsaPrivateKey returns a private key from the input x509 bytes
func UnmarshalRsaPrivateKey(b []byte) (PrivKey, error) {
	key, err := unmarshalOpensslPrivateKey(b)
	if err != nil {
		return nil, err
	}
	if 8*key.key.Size() < MinRsaKeyBits {
		return nil, ErrRsaKeyTooSmall
	}
	if key.Type() != RSA {
		return nil, errors.New("not actually an rsa public key")
	}
	return &RsaPrivateKey{key}, nil
}

// UnmarshalRsaPublicKey returns a public key from the input x509 bytes
func UnmarshalRsaPublicKey(b []byte) (PubKey, error) {
	key, err := unmarshalOpensslPublicKey(b)
	if err != nil {
		return nil, err
	}
	if 8*key.key.Size() < MinRsaKeyBits {
		return nil, ErrRsaKeyTooSmall
	}
	if key.Type() != RSA {
		return nil, errors.New("not actually an rsa public key")
	}
	return &RsaPublicKey{key}, nil
}
