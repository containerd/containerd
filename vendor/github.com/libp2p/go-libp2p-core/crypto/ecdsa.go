package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"io"
	"math/big"

	pb "github.com/libp2p/go-libp2p-core/crypto/pb"

	sha256 "github.com/minio/sha256-simd"
)

// ECDSAPrivateKey is an implementation of an ECDSA private key
type ECDSAPrivateKey struct {
	priv *ecdsa.PrivateKey
}

// ECDSAPublicKey is an implementation of an ECDSA public key
type ECDSAPublicKey struct {
	pub *ecdsa.PublicKey
}

// ECDSASig holds the r and s values of an ECDSA signature
type ECDSASig struct {
	R, S *big.Int
}

var (
	// ErrNotECDSAPubKey is returned when the public key passed is not an ecdsa public key
	ErrNotECDSAPubKey = errors.New("not an ecdsa public key")
	// ErrNilSig is returned when the signature is nil
	ErrNilSig = errors.New("sig is nil")
	// ErrNilPrivateKey is returned when a nil private key is provided
	ErrNilPrivateKey = errors.New("private key is nil")
	// ErrNilPublicKey is returned when a nil public key is provided
	ErrNilPublicKey = errors.New("public key is nil")
	// ECDSACurve is the default ecdsa curve used
	ECDSACurve = elliptic.P256()
)

// GenerateECDSAKeyPair generates a new ecdsa private and public key
func GenerateECDSAKeyPair(src io.Reader) (PrivKey, PubKey, error) {
	return GenerateECDSAKeyPairWithCurve(ECDSACurve, src)
}

// GenerateECDSAKeyPairWithCurve generates a new ecdsa private and public key with a speicified curve
func GenerateECDSAKeyPairWithCurve(curve elliptic.Curve, src io.Reader) (PrivKey, PubKey, error) {
	priv, err := ecdsa.GenerateKey(curve, src)
	if err != nil {
		return nil, nil, err
	}

	return &ECDSAPrivateKey{priv}, &ECDSAPublicKey{&priv.PublicKey}, nil
}

// ECDSAKeyPairFromKey generates a new ecdsa private and public key from an input private key
func ECDSAKeyPairFromKey(priv *ecdsa.PrivateKey) (PrivKey, PubKey, error) {
	if priv == nil {
		return nil, nil, ErrNilPrivateKey
	}

	return &ECDSAPrivateKey{priv}, &ECDSAPublicKey{&priv.PublicKey}, nil
}

// MarshalECDSAPrivateKey returns x509 bytes from a private key
func MarshalECDSAPrivateKey(ePriv ECDSAPrivateKey) ([]byte, error) {
	return x509.MarshalECPrivateKey(ePriv.priv)
}

// MarshalECDSAPublicKey returns x509 bytes from a public key
func MarshalECDSAPublicKey(ePub ECDSAPublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(ePub.pub)
}

// UnmarshalECDSAPrivateKey returns a private key from x509 bytes
func UnmarshalECDSAPrivateKey(data []byte) (PrivKey, error) {
	priv, err := x509.ParseECPrivateKey(data)
	if err != nil {
		return nil, err
	}

	return &ECDSAPrivateKey{priv}, nil
}

// UnmarshalECDSAPublicKey returns the public key from x509 bytes
func UnmarshalECDSAPublicKey(data []byte) (PubKey, error) {
	pubIfc, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		return nil, err
	}

	pub, ok := pubIfc.(*ecdsa.PublicKey)
	if !ok {
		return nil, ErrNotECDSAPubKey
	}

	return &ECDSAPublicKey{pub}, nil
}

// Bytes returns the private key as protobuf bytes
func (ePriv *ECDSAPrivateKey) Bytes() ([]byte, error) {
	return MarshalPrivateKey(ePriv)
}

// Type returns the key type
func (ePriv *ECDSAPrivateKey) Type() pb.KeyType {
	return pb.KeyType_ECDSA
}

// Raw returns x509 bytes from a private key
func (ePriv *ECDSAPrivateKey) Raw() ([]byte, error) {
	return x509.MarshalECPrivateKey(ePriv.priv)
}

// Equals compares two private keys
func (ePriv *ECDSAPrivateKey) Equals(o Key) bool {
	return basicEquals(ePriv, o)
}

// Sign returns the signature of the input data
func (ePriv *ECDSAPrivateKey) Sign(data []byte) ([]byte, error) {
	hash := sha256.Sum256(data)
	r, s, err := ecdsa.Sign(rand.Reader, ePriv.priv, hash[:])
	if err != nil {
		return nil, err
	}

	return asn1.Marshal(ECDSASig{
		R: r,
		S: s,
	})
}

// GetPublic returns a public key
func (ePriv *ECDSAPrivateKey) GetPublic() PubKey {
	return &ECDSAPublicKey{&ePriv.priv.PublicKey}
}

// Bytes returns the public key as protobuf bytes
func (ePub *ECDSAPublicKey) Bytes() ([]byte, error) {
	return MarshalPublicKey(ePub)
}

// Type returns the key type
func (ePub *ECDSAPublicKey) Type() pb.KeyType {
	return pb.KeyType_ECDSA
}

// Raw returns x509 bytes from a public key
func (ePub *ECDSAPublicKey) Raw() ([]byte, error) {
	return x509.MarshalPKIXPublicKey(ePub.pub)
}

// Equals compares to public keys
func (ePub *ECDSAPublicKey) Equals(o Key) bool {
	return basicEquals(ePub, o)
}

// Verify compares data to a signature
func (ePub *ECDSAPublicKey) Verify(data, sigBytes []byte) (bool, error) {
	sig := new(ECDSASig)
	if _, err := asn1.Unmarshal(sigBytes, sig); err != nil {
		return false, err
	}
	if sig == nil {
		return false, ErrNilSig
	}

	hash := sha256.Sum256(data)

	return ecdsa.Verify(ePub.pub, hash[:], sig.R, sig.S), nil
}
