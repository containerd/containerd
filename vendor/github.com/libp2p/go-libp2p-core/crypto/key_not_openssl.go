// +build !openssl

package crypto

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"

	btcec "github.com/btcsuite/btcd/btcec"
)

// KeyPairFromStdKey wraps standard library (and secp256k1) private keys in libp2p/go-libp2p-core/crypto keys
func KeyPairFromStdKey(priv crypto.PrivateKey) (PrivKey, PubKey, error) {
	if priv == nil {
		return nil, nil, ErrNilPrivateKey
	}

	switch p := priv.(type) {
	case *rsa.PrivateKey:
		return &RsaPrivateKey{*p}, &RsaPublicKey{k: p.PublicKey}, nil

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
	case *RsaPrivateKey:
		return &p.sk, nil
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
	case *RsaPublicKey:
		return &p.k, nil
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
