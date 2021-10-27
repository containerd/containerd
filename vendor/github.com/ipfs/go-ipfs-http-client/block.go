package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/interface-go-ipfs-core"
	caopts "github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/ipfs/interface-go-ipfs-core/path"
	mh "github.com/multiformats/go-multihash"
)

type BlockAPI HttpApi

type blockStat struct {
	Key   string
	BSize int `json:"Size"`

	cid cid.Cid
}

func (s *blockStat) Size() int {
	return s.BSize
}

func (s *blockStat) Path() path.Resolved {
	return path.IpldPath(s.cid)
}

func (api *BlockAPI) Put(ctx context.Context, r io.Reader, opts ...caopts.BlockPutOption) (iface.BlockStat, error) {
	options, _, err := caopts.BlockPutOptions(opts...)
	if err != nil {
		return nil, err
	}

	mht, ok := mh.Codes[options.MhType]
	if !ok {
		return nil, fmt.Errorf("unknowm mhType %d", options.MhType)
	}

	req := api.core().Request("block/put").
		Option("mhtype", mht).
		Option("mhlen", options.MhLength).
		Option("format", options.Codec).
		Option("pin", options.Pin).
		FileBody(r)

	var out blockStat
	if err := req.Exec(ctx, &out); err != nil {
		return nil, err
	}
	out.cid, err = cid.Parse(out.Key)
	if err != nil {
		return nil, err
	}

	return &out, nil
}

func (api *BlockAPI) Get(ctx context.Context, p path.Path) (io.Reader, error) {
	resp, err := api.core().Request("block/get", p.String()).Send(ctx)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	//TODO: make get return ReadCloser to avoid copying
	defer resp.Close()
	b := new(bytes.Buffer)
	if _, err := io.Copy(b, resp.Output); err != nil {
		return nil, err
	}

	return b, nil
}

func (api *BlockAPI) Rm(ctx context.Context, p path.Path, opts ...caopts.BlockRmOption) error {
	options, err := caopts.BlockRmOptions(opts...)
	if err != nil {
		return err
	}

	removedBlock := struct {
		Hash  string `json:",omitempty"`
		Error string `json:",omitempty"`
	}{}

	req := api.core().Request("block/rm").
		Option("force", options.Force).
		Arguments(p.String())

	if err := req.Exec(ctx, &removedBlock); err != nil {
		return err
	}

	if removedBlock.Error != "" {
		return errors.New(removedBlock.Error)
	}

	return nil
}

func (api *BlockAPI) Stat(ctx context.Context, p path.Path) (iface.BlockStat, error) {
	var out blockStat
	err := api.core().Request("block/stat", p.String()).Exec(ctx, &out)
	if err != nil {
		return nil, err
	}
	out.cid, err = cid.Parse(out.Key)
	if err != nil {
		return nil, err
	}

	return &out, nil
}

func (api *BlockAPI) core() *HttpApi {
	return (*HttpApi)(api)
}
