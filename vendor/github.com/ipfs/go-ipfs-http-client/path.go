package httpapi

import (
	"context"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	ipfspath "github.com/ipfs/go-path"
	"github.com/ipfs/interface-go-ipfs-core/path"
)

func (api *HttpApi) ResolvePath(ctx context.Context, p path.Path) (path.Resolved, error) {
	var out struct {
		Cid     cid.Cid
		RemPath string
	}

	//TODO: this is hacky, fixing https://github.com/ipfs/go-ipfs/issues/5703 would help

	var err error
	if p.Namespace() == "ipns" {
		if p, err = api.Name().Resolve(ctx, p.String()); err != nil {
			return nil, err
		}
	}

	if err := api.Request("dag/resolve", p.String()).Exec(ctx, &out); err != nil {
		return nil, err
	}

	// TODO:
	ipath, err := ipfspath.FromSegments("/"+p.Namespace()+"/", out.Cid.String(), out.RemPath)
	if err != nil {
		return nil, err
	}

	root, err := cid.Parse(ipfspath.Path(p.String()).Segments()[1])
	if err != nil {
		return nil, err
	}

	return path.NewResolvedPath(ipath, out.Cid, root, out.RemPath), nil
}

func (api *HttpApi) ResolveNode(ctx context.Context, p path.Path) (ipld.Node, error) {
	rp, err := api.ResolvePath(ctx, p)
	if err != nil {
		return nil, err
	}

	return api.Dag().Get(ctx, rp.Cid())
}
