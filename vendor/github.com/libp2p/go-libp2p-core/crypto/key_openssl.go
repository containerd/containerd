// +build openssl

package crypto

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"

	btcec "github.com/btcsuite/btcd/btcec"
	openssl "github.com/libp2p/go-openssl"
)

// KeyPairFromStdKey wraps standard library (and secp256k1) private keys in libp2p/go-libp2p-core/crypto keys
func KeyPairFromStdKey(priv crypto.PrivateKey) (PrivKey, PubKey, error) {
	if priv == nil {
		return nil, nil, ErrNilPrivateKey
	}

	switch p := priv.(type) {
	case *rsa.PrivateKey:
		pk, err := openssl.LoadPrivateKeyFromDER(x509.MarshalPKCS1PrivateKey(p))
		if err != nil {
			return nil, nil, err
		}

		return &opensslPrivateKey{pk}, &opensslPublicKey{key: pk}, nil

	case *ecdsa.PrivateKey:
		return &ECDSAPrivateKey{p}, &ECDSAPublicKey{&p.PublicKey}, nil

	case *ed25519.PrivateKey:
		pubIfc := p.Public()
		pub, _ := pubIfc.(ed25519.PublicKey)
		return &Ed25519PrivateKey{*p}, &Ed25519PublicKey{pub}, nil

	case *btcec.PrivateKey:
		sPriv := Secp256k1PrivateKey(*p)
		sPub := Secp256k1PublicKey(*p.PubKey())
		return &sPriv, &sPub, nil

	default:
		return nil, nil, ErrBadKeyType
	}
}

// PrivKeyToStdKey converts libp2p/go-libp2p-core/crypto private keys to standard library (and secp256k1) private keys
func PrivKeyToStdKey(priv PrivKey) (crypto.PrivateKey, error) {
	if priv == nil {
		return nil, ErrNilPrivateKey
	}
	switch p := priv.(type) {
	case *opensslPrivateKey:
		raw, err := p.Raw()
		if err != nil {
			return nil, err
		}
		return x509.ParsePKCS1PrivateKey(raw)
	case *ECDSAPrivateKey:
		return p.priv, nil
	case *Ed25519PrivateKey:
		return &p.k, nil
	case *Secp256k1PrivateKey:
		return p, nil
	default:
		return nil, ErrBadKeyType
	}
}

// PubKeyToStdKey converts libp2p/go-libp2p-core/crypto private keys to standard library (and secp256k1) public keys
func PubKeyToStdKey(pub PubKey) (crypto.PublicKey, error) {
	if pub == nil {
		return nil, ErrNilPublicKey
	}

	switch p := pub.(type) {
	case *opensslPublicKey:
		raw, err := p.Raw()
		if err != nil {
			return nil, err
		}
		return x509.ParsePKIXPublicKey(raw)
	case *ECDSAPublicKey:
		return p.pub, nil
	case *Ed25519PublicKey:
		return p.k, nil
	case *Secp256k1PublicKey:
		return p, nil
	default:
		return nil, ErrBadKeyType
	}
}
