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

package unpack

import (
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
)

func generateRandomDiffIDs(t testing.TB, num int) []digest.Digest {
	const size = 10
	diffIDs := make([]digest.Digest, 0, num)
	for i := 0; i < num; i++ {
		b := make([]byte, size)
		_, err := rand.Read(b)
		if err != nil {
			t.Fatalf("failed to generate random bytes: %v", err)
		}
		diffIDs = append(diffIDs, digest.FromBytes(b))
	}
	return diffIDs
}

func BenchmarkUnpackWithChainID(b *testing.B) {
	// This simulates the old way of repeatedly calculating per-layer chainID
	// as we unpack every layers, by calling `identity.ChainID`.
	unpackWithChainID := func(diffIDs []digest.Digest) {
		var chain []digest.Digest
		for i := 0; i < len(diffIDs); i++ {
			_ = identity.ChainID(chain) // parent layer chainID
			chain = append(chain, diffIDs[i])
			_ = identity.ChainID(chain).String() // current layer chainID
		}
		_ = identity.ChainID(chain).String()
	}

	numLayers := []int{5, 10, 25, 50}
	for _, sz := range numLayers {
		b.Run(fmt.Sprintf("num of layers: %d", sz), func(b *testing.B) {
			diffIDs := generateRandomDiffIDs(b, sz)
			for i := 0; i < b.N; i++ {
				unpackWithChainID(diffIDs)
			}
		})
	}
}

func BenchmarkUnpackWithChainIDs(b *testing.B) {
	// This simulates the new way of pre-calculating all chainIDs for every layer
	// by calling `identity.ChainIDs` once.
	unpackWithChainIDs := func(diffIDs []digest.Digest) {
		chainIDs := make([]digest.Digest, len(diffIDs))
		copy(chainIDs, diffIDs)
		chainIDs = identity.ChainIDs(chainIDs)
		for i := 0; i < len(diffIDs); i++ {
			if i > 0 {
				_ = chainIDs[i-1].String() // parent layer chainID
			}
			_ = chainIDs[i].String() // current layer chainID
		}
		if len(chainIDs) > 0 {
			_ = chainIDs[len(chainIDs)-1].String()
		}
	}

	numLayers := []int{5, 10, 25, 50}
	for _, sz := range numLayers {
		b.Run(fmt.Sprintf("num of layers: %d", sz), func(b *testing.B) {
			diffIDs := generateRandomDiffIDs(b, sz)
			for i := 0; i < b.N; i++ {
				unpackWithChainIDs(diffIDs)
			}
		})
	}
}
