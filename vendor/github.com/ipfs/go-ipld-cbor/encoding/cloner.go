package encoding

import (
	"sync"

	refmt "github.com/polydawn/refmt"
	"github.com/polydawn/refmt/obj/atlas"
)

// PooledCloner is a thread-safe pooled object cloner.
type PooledCloner struct {
	pool sync.Pool
}

// NewPooledCloner returns a PooledCloner with the given atlas. Do not copy
// after use.
func NewPooledCloner(atl atlas.Atlas) PooledCloner {
	return PooledCloner{
		pool: sync.Pool{
			New: func() interface{} {
				return refmt.NewCloner(atl)
			},
		},
	}
}

type selfCloner interface {
	Clone(b interface{}) error
}

// Clone clones a into b using a cloner from the pool.
func (p *PooledCloner) Clone(a, b interface{}) error {
	if self, ok := a.(selfCloner); ok {
		return self.Clone(b)
	}

	c := p.pool.Get().(refmt.Cloner)
	err := c.Clone(a, b)
	p.pool.Put(c)
	return err
}
