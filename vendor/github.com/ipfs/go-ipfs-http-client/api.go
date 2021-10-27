package httpapi

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	gohttp "net/http"
	"os"
	"path/filepath"
	"strings"

	iface "github.com/ipfs/interface-go-ipfs-core"
	caopts "github.com/ipfs/interface-go-ipfs-core/options"
	"github.com/mitchellh/go-homedir"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr-net"
)

const (
	DefaultPathName = ".ipfs"
	DefaultPathRoot = "~/" + DefaultPathName
	DefaultApiFile  = "api"
	EnvDir          = "IPFS_PATH"
)

// ErrApiNotFound if we fail to find a running daemon.
var ErrApiNotFound = errors.New("ipfs api address could not be found")

// HttpApi implements github.com/ipfs/interface-go-ipfs-core/CoreAPI using
// IPFS HTTP API.
//
// For interface docs see
// https://godoc.org/github.com/ipfs/interface-go-ipfs-core#CoreAPI
type HttpApi struct {
	url         string
	httpcli     gohttp.Client
	Headers     http.Header
	applyGlobal func(*requestBuilder)
}

// NewLocalApi tries to construct new HttpApi instance communicating with local
// IPFS daemon
//
// Daemon api address is pulled from the $IPFS_PATH/api file.
// If $IPFS_PATH env var is not present, it defaults to ~/.ipfs
func NewLocalApi() (*HttpApi, error) {
	baseDir := os.Getenv(EnvDir)
	if baseDir == "" {
		baseDir = DefaultPathRoot
	}

	return NewPathApi(baseDir)
}

// NewPathApi constructs new HttpApi by pulling api address from specified
// ipfspath. Api file should be located at $ipfspath/api
func NewPathApi(ipfspath string) (*HttpApi, error) {
	a, err := ApiAddr(ipfspath)
	if err != nil {
		if os.IsNotExist(err) {
			err = ErrApiNotFound
		}
		return nil, err
	}
	return NewApi(a)
}

// ApiAddr reads api file in specified ipfs path
func ApiAddr(ipfspath string) (ma.Multiaddr, error) {
	baseDir, err := homedir.Expand(ipfspath)
	if err != nil {
		return nil, err
	}

	apiFile := filepath.Join(baseDir, DefaultApiFile)

	api, err := ioutil.ReadFile(apiFile)
	if err != nil {
		return nil, err
	}

	return ma.NewMultiaddr(strings.TrimSpace(string(api)))
}

// NewApi constructs HttpApi with specified endpoint
func NewApi(a ma.Multiaddr) (*HttpApi, error) {
	c := &gohttp.Client{
		Transport: &gohttp.Transport{
			Proxy:             gohttp.ProxyFromEnvironment,
			DisableKeepAlives: true,
		},
	}

	return NewApiWithClient(a, c)
}

// NewApiWithClient constructs HttpApi with specified endpoint and custom http client
func NewApiWithClient(a ma.Multiaddr, c *gohttp.Client) (*HttpApi, error) {
	_, url, err := manet.DialArgs(a)
	if err != nil {
		return nil, err
	}

	if a, err := ma.NewMultiaddr(url); err == nil {
		_, host, err := manet.DialArgs(a)
		if err == nil {
			url = host
		}
	}

	return NewURLApiWithClient(url, c)
}

func NewURLApiWithClient(url string, c *gohttp.Client) (*HttpApi, error) {
	api := &HttpApi{
		url:         url,
		httpcli:     *c,
		Headers:     make(map[string][]string),
		applyGlobal: func(*requestBuilder) {},
	}

	// We don't support redirects.
	api.httpcli.CheckRedirect = func(_ *gohttp.Request, _ []*gohttp.Request) error {
		return fmt.Errorf("unexpected redirect")
	}
	return api, nil
}

func (api *HttpApi) WithOptions(opts ...caopts.ApiOption) (iface.CoreAPI, error) {
	options, err := caopts.ApiOptions(opts...)
	if err != nil {
		return nil, err
	}

	subApi := *api
	subApi.applyGlobal = func(req *requestBuilder) {
		if options.Offline {
			req.Option("offline", options.Offline)
		}
	}

	return &subApi, nil
}

func (api *HttpApi) Request(command string, args ...string) RequestBuilder {
	headers := make(map[string]string)
	if api.Headers != nil {
		for k := range api.Headers {
			headers[k] = api.Headers.Get(k)
		}
	}
	return &requestBuilder{
		command: command,
		args:    args,
		shell:   api,
		headers: headers,
	}
}

func (api *HttpApi) Unixfs() iface.UnixfsAPI {
	return (*UnixfsAPI)(api)
}

func (api *HttpApi) Block() iface.BlockAPI {
	return (*BlockAPI)(api)
}

func (api *HttpApi) Dag() iface.APIDagService {
	return (*HttpDagServ)(api)
}

func (api *HttpApi) Name() iface.NameAPI {
	return (*NameAPI)(api)
}

func (api *HttpApi) Key() iface.KeyAPI {
	return (*KeyAPI)(api)
}

func (api *HttpApi) Pin() iface.PinAPI {
	return (*PinAPI)(api)
}

func (api *HttpApi) Object() iface.ObjectAPI {
	return (*ObjectAPI)(api)
}

func (api *HttpApi) Dht() iface.DhtAPI {
	return (*DhtAPI)(api)
}

func (api *HttpApi) Swarm() iface.SwarmAPI {
	return (*SwarmAPI)(api)
}

func (api *HttpApi) PubSub() iface.PubSubAPI {
	return (*PubsubAPI)(api)
}
