package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/ipfs/go-merkledag"
	dag "github.com/ipfs/go-merkledag"
	ft "github.com/ipfs/go-unixfs"
	"github.com/ipfs/interface-go-ipfs-core"
	caopts "github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/ipfs/interface-go-ipfs-core/path"
)

type ObjectAPI HttpApi

type objectOut struct {
	Hash string
}

func (api *ObjectAPI) New(ctx context.Context, opts ...caopts.ObjectNewOption) (ipld.Node, error) {
	options, err := caopts.ObjectNewOptions(opts...)
	if err != nil {
		return nil, err
	}

	var n ipld.Node
	switch options.Type {
	case "empty":
		n = new(dag.ProtoNode)
	case "unixfs-dir":
		n = ft.EmptyDirNode()
	default:
		return nil, fmt.Errorf("unknown object type: %s", options.Type)
	}

	return n, nil
}

func (api *ObjectAPI) Put(ctx context.Context, r io.Reader, opts ...caopts.ObjectPutOption) (path.Resolved, error) {
	options, err := caopts.ObjectPutOptions(opts...)
	if err != nil {
		return nil, err
	}

	var out objectOut
	err = api.core().Request("object/put").
		Option("inputenc", options.InputEnc).
		Option("datafieldenc", options.DataType).
		Option("pin", options.Pin).
		FileBody(r).
		Exec(ctx, &out)
	if err != nil {
		return nil, err
	}

	c, err := cid.Parse(out.Hash)
	if err != nil {
		return nil, err
	}

	return path.IpfsPath(c), nil
}

func (api *ObjectAPI) Get(ctx context.Context, p path.Path) (ipld.Node, error) {
	r, err := api.core().Block().Get(ctx, p)
	if err != nil {
		return nil, err
	}
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return merkledag.DecodeProtobuf(b)
}

func (api *ObjectAPI) Data(ctx context.Context, p path.Path) (io.Reader, error) {
	resp, err := api.core().Request("object/data", p.String()).Send(ctx)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	//TODO: make Data return ReadCloser to avoid copying
	defer resp.Close()
	b := new(bytes.Buffer)
	if _, err := io.Copy(b, resp.Output); err != nil {
		return nil, err
	}

	return b, nil
}

func (api *ObjectAPI) Links(ctx context.Context, p path.Path) ([]*ipld.Link, error) {
	var out struct {
		Links []struct {
			Name string
			Hash string
			Size uint64
		}
	}
	if err := api.core().Request("object/links", p.String()).Exec(ctx, &out); err != nil {
		return nil, err
	}
	res := make([]*ipld.Link, len(out.Links))
	for i, l := range out.Links {
		c, err := cid.Parse(l.Hash)
		if err != nil {
			return nil, err
		}

		res[i] = &ipld.Link{
			Cid:  c,
			Name: l.Name,
			Size: l.Size,
		}
	}

	return res, nil
}

func (api *ObjectAPI) Stat(ctx context.Context, p path.Path) (*iface.ObjectStat, error) {
	var out struct {
		Hash           string
		NumLinks       int
		BlockSize      int
		LinksSize      int
		DataSize       int
		CumulativeSize int
	}
	if err := api.core().Request("object/stat", p.String()).Exec(ctx, &out); err != nil {
		return nil, err
	}

	c, err := cid.Parse(out.Hash)
	if err != nil {
		return nil, err
	}

	return &iface.ObjectStat{
		Cid:            c,
		NumLinks:       out.NumLinks,
		BlockSize:      out.BlockSize,
		LinksSize:      out.LinksSize,
		DataSize:       out.DataSize,
		CumulativeSize: out.CumulativeSize,
	}, nil
}

func (api *ObjectAPI) AddLink(ctx context.Context, base path.Path, name string, child path.Path, opts ...caopts.ObjectAddLinkOption) (path.Resolved, error) {
	options, err := caopts.ObjectAddLinkOptions(opts...)
	if err != nil {
		return nil, err
	}

	var out objectOut
	err = api.core().Request("object/patch/add-link", base.String(), name, child.String()).
		Option("create", options.Create).
		Exec(ctx, &out)
	if err != nil {
		return nil, err
	}

	c, err := cid.Parse(out.Hash)
	if err != nil {
		return nil, err
	}

	return path.IpfsPath(c), nil
}

func (api *ObjectAPI) RmLink(ctx context.Context, base path.Path, link string) (path.Resolved, error) {
	var out objectOut
	err := api.core().Request("object/patch/rm-link", base.String(), link).
		Exec(ctx, &out)
	if err != nil {
		return nil, err
	}

	c, err := cid.Parse(out.Hash)
	if err != nil {
		return nil, err
	}

	return path.IpfsPath(c), nil
}

func (api *ObjectAPI) AppendData(ctx context.Context, p path.Path, r io.Reader) (path.Resolved, error) {
	var out objectOut
	err := api.core().Request("object/patch/append-data", p.String()).
		FileBody(r).
		Exec(ctx, &out)
	if err != nil {
		return nil, err
	}

	c, err := cid.Parse(out.Hash)
	if err != nil {
		return nil, err
	}

	return path.IpfsPath(c), nil
}

func (api *ObjectAPI) SetData(ctx context.Context, p path.Path, r io.Reader) (path.Resolved, error) {
	var out objectOut
	err := api.core().Request("object/patch/set-data", p.String()).
		FileBody(r).
		Exec(ctx, &out)
	if err != nil {
		return nil, err
	}

	c, err := cid.Parse(out.Hash)
	if err != nil {
		return nil, err
	}

	return path.IpfsPath(c), nil
}

type change struct {
	Type   iface.ChangeType
	Path   string
	Before cid.Cid
	After  cid.Cid
}

func (api *ObjectAPI) Diff(ctx context.Context, a path.Path, b path.Path) ([]iface.ObjectChange, error) {
	var out struct {
		Changes []change
	}
	if err := api.core().Request("object/diff", a.String(), b.String()).Exec(ctx, &out); err != nil {
		return nil, err
	}
	res := make([]iface.ObjectChange, len(out.Changes))
	for i, ch := range out.Changes {
		res[i] = iface.ObjectChange{
			Type: ch.Type,
			Path: ch.Path,
		}
		if ch.Before != cid.Undef {
			res[i].Before = path.IpfsPath(ch.Before)
		}
		if ch.After != cid.Undef {
			res[i].After = path.IpfsPath(ch.After)
		}
	}
	return res, nil
}

func (api *ObjectAPI) core() *HttpApi {
	return (*HttpApi)(api)
}
