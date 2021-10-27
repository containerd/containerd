package format

import (
	"context"

	cid "github.com/ipfs/go-cid"
)

// GetLinks returns the CIDs of the children of the given node. Prefer this
// method over looking up the node itself and calling `Links()` on it as this
// method may be able to use a link cache.
func GetLinks(ctx context.Context, ng NodeGetter, c cid.Cid) ([]*Link, error) {
	if c.Type() == cid.Raw {
		return nil, nil
	}
	if gl, ok := ng.(LinkGetter); ok {
		return gl.GetLinks(ctx, c)
	}
	node, err := ng.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	return node.Links(), nil
}

// GetDAG will fill out all of the links of the given Node.
// It returns an array of NodePromise with the linked nodes all in the proper
// order.
func GetDAG(ctx context.Context, ds NodeGetter, root Node) []*NodePromise {
	var cids []cid.Cid
	for _, lnk := range root.Links() {
		cids = append(cids, lnk.Cid)
	}

	return GetNodes(ctx, ds, cids)
}

// GetNodes returns an array of 'FutureNode' promises, with each corresponding
// to the key with the same index as the passed in keys
func GetNodes(ctx context.Context, ds NodeGetter, keys []cid.Cid) []*NodePromise {

	// Early out if no work to do
	if len(keys) == 0 {
		return nil
	}

	promises := make([]*NodePromise, len(keys))
	for i := range keys {
		promises[i] = NewNodePromise(ctx)
	}

	dedupedKeys := dedupeKeys(keys)
	go func() {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		nodechan := ds.GetMany(ctx, dedupedKeys)

		for count := 0; count < len(keys); {
			select {
			case opt, ok := <-nodechan:
				if !ok {
					for _, p := range promises {
						p.Fail(ErrNotFound)
					}
					return
				}

				if opt.Err != nil {
					for _, p := range promises {
						p.Fail(opt.Err)
					}
					return
				}

				nd := opt.Node
				c := nd.Cid()
				for i, lnk_c := range keys {
					if c.Equals(lnk_c) {
						count++
						promises[i].Send(nd)
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return promises
}

func Copy(ctx context.Context, from, to DAGService, root cid.Cid) error {
	node, err := from.Get(ctx, root)
	if err != nil {
		return err
	}
	links := node.Links()
	for _, link := range links {
		err := Copy(ctx, from, to, link.Cid)
		if err != nil {
			return err
		}
	}
	err = to.Add(ctx, node)
	if err != nil {
		return err
	}
	return nil
}

// Remove duplicates from a list of keys
func dedupeKeys(cids []cid.Cid) []cid.Cid {
	set := cid.NewSet()
	for _, c := range cids {
		set.Add(c)
	}
	return set.Keys()
}
