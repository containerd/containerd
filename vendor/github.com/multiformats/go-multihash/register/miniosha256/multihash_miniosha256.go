/*
	This package has no purpose except to perform registration of multihashes.

	It is meant to be used as a side-effecting import, e.g.

		import (
			_ "github.com/multiformats/go-multihash/register/miniosha256"
		)

	This package registers alternative implementations for sha2-256, using
	the github.com/minio/sha256-simd library.
*/
package miniosha256

import (
	"github.com/minio/sha256-simd"

	"github.com/multiformats/go-multihash/core"
)

func init() {
	multihash.Register(multihash.SHA2_256, sha256.New)
}
