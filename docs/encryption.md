# containerd image encryption

The containerd encryption feature allows the encryption and decryption of a container image.
It is based on a [proposal for an OCI specification](https://github.com/opencontainers/image-spec/issues/747) to define the structure of an encrypted image.
The encryption is specified on the layer, allowing the encryption of specific layers within an image. For example, given an image with an ubuntu, node and some custom code on top, only the custom code (top-most layer) can be encrypted to still benefit from layer deduplication of insensitive data. The key sharing is done via wrapped keys in the metadata.
Therefore, an encrypted image data intended for multiple recipients can also be deduplicated.
All this is done via the addition of additional layer mediatype `+enc`.
More details can be viewed in the [design doc](https://docs.google.com/document/d/146Eaj7_r1B0Q_2KylVHbXhxcuogsnlSbqjwGTORB8iw).

The two main usage points are in the creation of an encrypted image, and the decryption of the image upon usage.
As most of the integration points would be transparent or consumed by other containerd runtime components or other utilities like buildkit, we have created several `ctr` commands to better illustrate the usage of the encryption feature.

# Example End User Usage

We have added 3 commands in the [`ctr`](https://github.com/containerd/containerd/tree/master/cmd/ctr) client under the image module. They are:
- `ctr image encrypt`
- `ctr image decrypt`
- `ctr image layerinfo`

## Encrypt

The following command performs an encryption of the image `docker.io/library/alpine:latest` to an encrypted image with the tag `docker.io/library/alpine:enc`.
The encryption is done for two recipients with the public key of `/tmp/tmp.AGrSDkaSad/mypubkey.pem` (jwe) and `/tmp/tmp.AGrSDkaSad/clientcert.pem` (pkcs7).
The option `--layer -1` specifies the layer filter for encryption, -1 indicating the top-most layer should be encrypted.

```
$ ctr images encrypt \
    --recipient /tmp/tmp.AGrSDkaSad/mypubkey.pem \
    --recipient /tmp/tmp.AGrSDkaSad/clientcert.pem \
    --layer -1 \
    docker.io/library/alpine:latest docker.io/library/alpine:enc

Encrypting docker.io/library/alpine:latest to docker.io/library/alpine:enc
```

## Layerinfo

The layerinfo command provides information about the encryption status of an image. In the following command, we use it to inspect the encryption metadata.

```
$ ctr images layerinfo docker.io/library/alpine:enc
   #                                                                    DIGEST         PLATFORM      SIZE   ENCRYPTION   RECIPIENTS
   0   sha256:3427d6934e7749d556be6881a17265c9817abc6447df80a09c8eecc465c5bfb3      linux/amd64   2206947
   0   sha256:d9a094b6b49fc760501d44ae96f19284e86db0a51b979756ca8a0df4a2746c79     linux/arm/v6   2146469
   1   sha256:ef87d8b3048d8f1f7af7605328f63aab078a1433116dc15738989551184d7a87     linux/arm/v6       191   jwe,pkcs7    [jwe], [pkcs7]
   0   sha256:4b0872dff46806a4037c5f158d1d17d5252c9e1f421b7c61445f1a64f6a853a8   linux/arm64/v8   2099778
   1   sha256:fe022206e6848082f9c1d6e69974157af70ad56bf8698d89e1641d4598bf8ce9   linux/arm64/v8       192   jwe,pkcs7    [jwe], [pkcs7]
   0   sha256:d1fceb26d4a2dc1f30d05fd0f9567edb5997d504f044ad6486aecc3d5aaa9b4e        linux/386   2271476
   1   sha256:383a3d4c6789667dbfb6b3742492c4a925315e750f99a5d664ff72f2bb0ae659        linux/386       191   jwe,pkcs7    [jwe], [pkcs7]
   0   sha256:8aea19b10fd75004ab8fd2d02df719c06528ad3539e686a2d26c933d53f25675    linux/ppc64le   2195242
   1   sha256:965a60ab5513a2eee33f4d960b63ee347215eb31d06a4ed61f6d90d209462d76    linux/ppc64le       193   jwe,pkcs7    [jwe], [pkcs7]
   0   sha256:783541963cb4e52173193fe947bb7a7f7e5a6657a4cbbb6b8b077bbee7255605      linux/s390x   2307762
   1   sha256:69d3260b3f5430ade9a3ee0f1b71a32a8e4ef268552beeae29930a8795dc54bf      linux/s390x       192   jwe,pkcs7    [jwe], [pkcs7]
```

## Decrypt

The following command performs an decryption of the encrypted image `docker.io/library/alpine:enc` to the image tag `docker.io/library/alpine:dec`.
The decryption is done by passing in the private key that corresponds to at least one of the recipients of the encrypted image.

```
$ ctr images decrypt \
    --key /tmp/tmp.AGrSDkaSad/mykey2.pem \
    docker.io/library/alpine:enc docker.io/library/alpine:dec

Decrypting docker.io/library/alpine:enc to docker.io/library/alpine:dec
```

# Other Consumers

Other consumers of the encryption routine can include the [containerd diff plugin](https://github.com/containerd/containerd/tree/master/services/diff) to have the decryption be performed in the pull path, as well as the docker CLI or other build tools that wish to provide an encryption option.
The current draft of exposed interfaces we believe will be used by consumers are as follows:

```
/* Functions */

// EncryptImage encrypts an image; it accepts either an OCI descriptor representing a manifest list or a single manifest
func EncryptImage(ctx context.Context, cs content.Store, ls leases.Manager, l leases.Lease, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *LayerFilter) (ocispec.Descriptor, bool, error)

// DecryptImage decrypts an image; it accepts either an OCI descriptor representing a manifest list or a single manifest
func DecryptImage(ctx context.Context, cs content.Store, ls leases.Manager, l leases.Lease, desc ocispec.Descriptor, cc *encconfig.CryptoConfig, lf *LayerFilter) (ocispec.Descriptor, bool, error)

// DecryptLayer decrypts the layer using the CryptoConfig and creates a new OCI Descriptor.
// The caller is expected to store the returned plain data and OCI Descriptor
func DecryptLayer(cc *encconfig.CryptoConfig, dataReader content.ReaderAt, desc ocispec.Descriptor, unwrapOnly bool) (ocispec.Descriptor, io.Reader, error)

// CheckAuthorization checks whether a user has the right keys to be allowed to access an image (every layer)
// It takes decrypting of the layers only as far as decrypting the asymmetrically encrypted data
// The decryption is only done for the current platform
func CheckAuthorization(ctx context.Context, cs content.Store, desc ocispec.Descriptor, dc *encconfig.DecryptConfig) error

// GetImageLayerDescriptors gets the image layer Descriptors of an image; the array contains
// a list of Descriptors belonging to one platform followed by lists of other platforms
func GetImageLayerDescriptors(ctx context.Context, cs content.Store, desc ocispec.Descriptor) ([]ocispec.Descriptor, error)

/* Cryptography Configuration Datastructures */

// CryptoConfig is a common wrapper for EncryptConfig and DecrypConfig that can
// be passed through functions that share much code for encryption and decryption
type CryptoConfig struct {
	Ec *EncryptConfig
	Dc *DecryptConfig
}

// EncryptConfig is the container image PGP encryption configuration holding
// the identifiers of those that will be able to decrypt the container and
// the PGP public keyring file data that contains their public keys.
type EncryptConfig struct {
	// map holding 'gpg-recipients', 'gpg-pubkeyringfile', 'pubkeys', 'x509s'
	Parameters map[string][][]byte

	// for adding recipients on an already encrypted image we need the
	// symmetric keys for the layers so we can wrap them with the recpient's
	// public key
	Operation int32 // currently only OperationAddRecipients is supported, if at all
	Dc        DecryptConfig
}

// DecryptConfig wraps the Parameters map that holds the decryption key
type DecryptConfig struct {
	// map holding 'privkeys', 'x509s', 'gpg-privatekeys'
	Parameters map[string][][]byte
}
```
