package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"

	"github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-ipld-format"
	"github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/ipfs/interface-go-ipfs-core/path"
)

type httpNodeAdder HttpApi
type HttpDagServ httpNodeAdder
type pinningHttpNodeAdder httpNodeAdder

func (api *HttpDagServ) Get(ctx context.Context, c cid.Cid) (format.Node, error) {
	r, err := api.core().Block().Get(ctx, path.IpldPath(c))
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	blk, err := blocks.NewBlockWithCid(data, c)
	if err != nil {
		return nil, err
	}

	return format.DefaultBlockDecoder.Decode(blk)
}

func (api *HttpDagServ) GetMany(ctx context.Context, cids []cid.Cid) <-chan *format.NodeOption {
	out := make(chan *format.NodeOption)

	for _, c := range cids {
		// TODO: Consider limiting concurrency of this somehow
		go func(c cid.Cid) {
			n, err := api.Get(ctx, c)

			select {
			case out <- &format.NodeOption{Node: n, Err: err}:
			case <-ctx.Done():
			}
		}(c)
	}
	return out
}

func (api *httpNodeAdder) add(ctx context.Context, nd format.Node, pin bool) error {
	c := nd.Cid()
	prefix := c.Prefix()
	format := cid.CodecToStr[prefix.Codec]
	if prefix.Version == 0 {
		format = "v0"
	}

	stat, err := api.core().Block().Put(ctx, bytes.NewReader(nd.RawData()),
		options.Block.Hash(prefix.MhType, prefix.MhLength),
		options.Block.Format(format),
		options.Block.Pin(pin))
	if err != nil {
		return err
	}
	if !stat.Path().Cid().Equals(c) {
		return fmt.Errorf("cids didn't match - local %s, remote %s", c.String(), stat.Path().Cid().String())
	}
	return nil
}

func (api *httpNodeAdder) addMany(ctx context.Context, nds []format.Node, pin bool) error {
	for _, nd := range nds {
		// TODO: optimize
		if err := api.add(ctx, nd, pin); err != nil {
			return err
		}
	}
	return nil
}

func (api *HttpDagServ) AddMany(ctx context.Context, nds []format.Node) error {
	return (*httpNodeAdder)(api).addMany(ctx, nds, false)
}

func (api *HttpDagServ) Add(ctx context.Context, nd format.Node) error {
	return (*httpNodeAdder)(api).add(ctx, nd, false)
}

func (api *pinningHttpNodeAdder) Add(ctx context.Context, nd format.Node) error {
	return (*httpNodeAdder)(api).add(ctx, nd, true)
}

func (api *pinningHttpNodeAdder) AddMany(ctx context.Context, nds []format.Node) error {
	return (*httpNodeAdder)(api).addMany(ctx, nds, true)
}

func (api *HttpDagServ) Pinning() format.NodeAdder {
	return (*pinningHttpNodeAdder)(api)
}

func (api *HttpDagServ) Remove(ctx context.Context, c cid.Cid) error {
	return api.core().Block().Rm(ctx, path.IpldPath(c)) //TODO: should we force rm?
}

func (api *HttpDagServ) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	for _, c := range cids {
		// TODO: optimize
		if err := api.Remove(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (api *httpNodeAdder) core() *HttpApi {
	return (*HttpApi)(api)
}

func (api *HttpDagServ) core() *HttpApi {
	return (*HttpApi)(api)
}
