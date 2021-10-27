/*
	This package has no purpose except to perform registration of mulithashes.

	It is meant to be used as a side-effecting import, e.g.

		import (
			_ "github.com/multiformats/go-multihash/register/all"
		)

	This package registers many multihashes at once.
	Importing it will increase the size of your dependency tree significantly.
	It's recommended that you import this package if you're building some
	kind of data broker application, which may need to handle many different kinds of hashes;
	if you're building an application which you know only handles a specific hash,
	importing this package may bloat your builds unnecessarily.
*/
package all

import (
	_ "github.com/multiformats/go-multihash/register/blake2"
	_ "github.com/multiformats/go-multihash/register/sha3"
)
