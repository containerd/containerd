package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ipfs/interface-go-ipfs-core"
	caopts "github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/ipfs/interface-go-ipfs-core/options/namesys"
	"github.com/ipfs/interface-go-ipfs-core/path"
)

type NameAPI HttpApi

type ipnsEntry struct {
	JName  string `json:"Name"`
	JValue string `json:"Value"`

	path path.Path
}

func (e *ipnsEntry) Name() string {
	return e.JName
}

func (e *ipnsEntry) Value() path.Path {
	return e.path
}

func (api *NameAPI) Publish(ctx context.Context, p path.Path, opts ...caopts.NamePublishOption) (iface.IpnsEntry, error) {
	options, err := caopts.NamePublishOptions(opts...)
	if err != nil {
		return nil, err
	}

	req := api.core().Request("name/publish", p.String()).
		Option("key", options.Key).
		Option("allow-offline", options.AllowOffline).
		Option("lifetime", options.ValidTime).
		Option("resolve", false)

	if options.TTL != nil {
		req.Option("ttl", options.TTL)
	}

	var out ipnsEntry
	if err := req.Exec(ctx, &out); err != nil {
		return nil, err
	}
	out.path = path.New(out.JValue)
	return &out, out.path.IsValid()
}

func (api *NameAPI) Search(ctx context.Context, name string, opts ...caopts.NameResolveOption) (<-chan iface.IpnsResult, error) {
	options, err := caopts.NameResolveOptions(opts...)
	if err != nil {
		return nil, err
	}

	ropts := nsopts.ProcessOpts(options.ResolveOpts)
	if ropts.Depth != nsopts.DefaultDepthLimit && ropts.Depth != 1 {
		return nil, fmt.Errorf("Name.Resolve: depth other than 1 or %d not supported", nsopts.DefaultDepthLimit)
	}

	req := api.core().Request("name/resolve", name).
		Option("nocache", !options.Cache).
		Option("recursive", ropts.Depth != 1).
		Option("dht-record-count", ropts.DhtRecordCount).
		Option("dht-timeout", ropts.DhtTimeout).
		Option("stream", true)
	resp, err := req.Send(ctx)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}

	res := make(chan iface.IpnsResult)

	go func() {
		defer close(res)
		defer resp.Close()

		dec := json.NewDecoder(resp.Output)

		for {
			var out struct{ Path string }
			err := dec.Decode(&out)
			if err == io.EOF {
				return
			}
			var ires iface.IpnsResult
			if err == nil {
				ires.Path = path.New(out.Path)
			}

			select {
			case res <- ires:
			case <-ctx.Done():
			}
			if err != nil {
				return
			}
		}
	}()

	return res, nil
}

func (api *NameAPI) Resolve(ctx context.Context, name string, opts ...caopts.NameResolveOption) (path.Path, error) {
	options, err := caopts.NameResolveOptions(opts...)
	if err != nil {
		return nil, err
	}

	ropts := nsopts.ProcessOpts(options.ResolveOpts)
	if ropts.Depth != nsopts.DefaultDepthLimit && ropts.Depth != 1 {
		return nil, fmt.Errorf("Name.Resolve: depth other than 1 or %d not supported", nsopts.DefaultDepthLimit)
	}

	req := api.core().Request("name/resolve", name).
		Option("nocache", !options.Cache).
		Option("recursive", ropts.Depth != 1).
		Option("dht-record-count", ropts.DhtRecordCount).
		Option("dht-timeout", ropts.DhtTimeout)

	var out struct{ Path string }
	if err := req.Exec(ctx, &out); err != nil {
		return nil, err
	}

	return path.New(out.Path), nil
}

func (api *NameAPI) core() *HttpApi {
	return (*HttpApi)(api)
}
