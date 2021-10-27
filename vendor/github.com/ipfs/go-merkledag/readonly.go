package merkledag

import (
	"fmt"

	ipld "github.com/ipfs/go-ipld-format"
)

// ErrReadOnly is used when a read-only datastructure is written to.
var ErrReadOnly = fmt.Errorf("cannot write to readonly DAGService")

// NewReadOnlyDagService takes a NodeGetter, and returns a full DAGService
// implementation that returns ErrReadOnly when its 'write' methods are
// invoked.
func NewReadOnlyDagService(ng ipld.NodeGetter) ipld.DAGService {
	return &ComboService{
		Read:  ng,
		Write: &ErrorService{ErrReadOnly},
	}
}
