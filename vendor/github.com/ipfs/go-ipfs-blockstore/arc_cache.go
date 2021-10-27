package blockstore

import (
	"context"

	lru "github.com/hashicorp/golang-lru"
	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	metrics "github.com/ipfs/go-metrics-interface"
)

type cacheHave bool
type cacheSize int

// arccache wraps a BlockStore with an Adaptive Replacement Cache (ARC) for
// block Cids. This provides block access-time improvements, allowing
// to short-cut many searches without query-ing the underlying datastore.
type arccache struct {
	arc        *lru.TwoQueueCache
	blockstore Blockstore

	hits  metrics.Counter
	total metrics.Counter
}

func newARCCachedBS(ctx context.Context, bs Blockstore, lruSize int) (*arccache, error) {
	arc, err := lru.New2Q(lruSize)
	if err != nil {
		return nil, err
	}
	c := &arccache{arc: arc, blockstore: bs}
	c.hits = metrics.NewCtx(ctx, "arc.hits_total", "Number of ARC cache hits").Counter()
	c.total = metrics.NewCtx(ctx, "arc_total", "Total number of ARC cache requests").Counter()

	return c, nil
}

func (b *arccache) DeleteBlock(k cid.Cid) error {
	if has, _, ok := b.hasCached(k); ok && !has {
		return nil
	}

	b.arc.Remove(k) // Invalidate cache before deleting.
	err := b.blockstore.DeleteBlock(k)
	if err == nil {
		b.cacheHave(k, false)
	}
	return err
}

// if ok == false has is inconclusive
// if ok == true then has respons to question: is it contained
func (b *arccache) hasCached(k cid.Cid) (has bool, size int, ok bool) {
	b.total.Inc()
	if !k.Defined() {
		log.Error("undefined cid in arccache")
		// Return cache invalid so the call to blockstore happens
		// in case of invalid key and correct error is created.
		return false, -1, false
	}

	h, ok := b.arc.Get(k.KeyString())
	if ok {
		b.hits.Inc()
		switch h := h.(type) {
		case cacheHave:
			return bool(h), -1, true
		case cacheSize:
			return true, int(h), true
		}
	}
	return false, -1, false
}

func (b *arccache) Has(k cid.Cid) (bool, error) {
	if has, _, ok := b.hasCached(k); ok {
		return has, nil
	}
	has, err := b.blockstore.Has(k)
	if err != nil {
		return false, err
	}
	b.cacheHave(k, has)
	return has, nil
}

func (b *arccache) GetSize(k cid.Cid) (int, error) {
	if has, blockSize, ok := b.hasCached(k); ok {
		if !has {
			// don't have it, return
			return -1, ErrNotFound
		}
		if blockSize >= 0 {
			// have it and we know the size
			return blockSize, nil
		}
		// we have it but don't know the size, ask the datastore.
	}
	blockSize, err := b.blockstore.GetSize(k)
	if err == ErrNotFound {
		b.cacheHave(k, false)
	} else if err == nil {
		b.cacheSize(k, blockSize)
	}
	return blockSize, err
}

func (b *arccache) Get(k cid.Cid) (blocks.Block, error) {
	if !k.Defined() {
		log.Error("undefined cid in arc cache")
		return nil, ErrNotFound
	}

	if has, _, ok := b.hasCached(k); ok && !has {
		return nil, ErrNotFound
	}

	bl, err := b.blockstore.Get(k)
	if bl == nil && err == ErrNotFound {
		b.cacheHave(k, false)
	} else if bl != nil {
		b.cacheSize(k, len(bl.RawData()))
	}
	return bl, err
}

func (b *arccache) Put(bl blocks.Block) error {
	if has, _, ok := b.hasCached(bl.Cid()); ok && has {
		return nil
	}

	err := b.blockstore.Put(bl)
	if err == nil {
		b.cacheSize(bl.Cid(), len(bl.RawData()))
	}
	return err
}

func (b *arccache) PutMany(bs []blocks.Block) error {
	var good []blocks.Block
	for _, block := range bs {
		// call put on block if result is inconclusive or we are sure that
		// the block isn't in storage
		if has, _, ok := b.hasCached(block.Cid()); !ok || (ok && !has) {
			good = append(good, block)
		}
	}
	err := b.blockstore.PutMany(good)
	if err != nil {
		return err
	}
	for _, block := range good {
		b.cacheSize(block.Cid(), len(block.RawData()))
	}
	return nil
}

func (b *arccache) HashOnRead(enabled bool) {
	b.blockstore.HashOnRead(enabled)
}

func (b *arccache) cacheHave(c cid.Cid, have bool) {
	b.arc.Add(c.KeyString(), cacheHave(have))
}

func (b *arccache) cacheSize(c cid.Cid, blockSize int) {
	b.arc.Add(c.KeyString(), cacheSize(blockSize))
}

func (b *arccache) AllKeysChan(ctx context.Context) (<-chan cid.Cid, error) {
	return b.blockstore.AllKeysChan(ctx)
}

func (b *arccache) GCLock() Unlocker {
	return b.blockstore.(GCBlockstore).GCLock()
}

func (b *arccache) PinLock() Unlocker {
	return b.blockstore.(GCBlockstore).PinLock()
}

func (b *arccache) GCRequested() bool {
	return b.blockstore.(GCBlockstore).GCRequested()
}
