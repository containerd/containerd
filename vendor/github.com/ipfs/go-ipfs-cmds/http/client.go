package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/ipfs/go-ipfs-cmds"

	"github.com/ipfs/go-ipfs-files"
)

const (
	ApiUrlFormat = "%s%s/%s?%s"
)

var OptionSkipMap = map[string]bool{
	"api": true,
}

type client struct {
	serverAddress string
	httpClient    *http.Client
	ua            string
	apiPrefix     string
	fallback      cmds.Executor
}

// ClientOpt is an option that can be passed to the HTTP client constructor.
type ClientOpt func(*client)

// ClientWithUserAgent specifies the HTTP user agent for the client.
func ClientWithUserAgent(ua string) ClientOpt {
	return func(c *client) {
		c.ua = ua
	}
}

// ClientWithHTTPClient specifies a custom http.Client. Defaults to
// http.DefaultClient.
func ClientWithHTTPClient(hc *http.Client) ClientOpt {
	return func(c *client) {
		c.httpClient = hc
	}
}

// ClientWithAPIPrefix specifies an API URL prefix.
func ClientWithAPIPrefix(apiPrefix string) ClientOpt {
	return func(c *client) {
		c.apiPrefix = apiPrefix
	}
}

// ClientWithFallback adds a fallback executor to the client.
//
// Note: This may run the PreRun function twice.
func ClientWithFallback(exe cmds.Executor) ClientOpt {
	return func(c *client) {
		c.fallback = exe
	}
}

// NewClient constructs a new HTTP-backed command executor.
func NewClient(address string, opts ...ClientOpt) cmds.Executor {
	if !strings.HasPrefix(address, "http://") {
		address = "http://" + address
	}

	c := &client{
		serverAddress: address,
		httpClient:    http.DefaultClient,
		ua:            "go-ipfs-cmds/http",
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c *client) Execute(req *cmds.Request, re cmds.ResponseEmitter, env cmds.Environment) error {
	cmd := req.Command

	err := cmd.CheckArguments(req)
	if err != nil {
		return err
	}

	if cmd.PreRun != nil {
		err := cmd.PreRun(req, env)
		if err != nil {
			return err
		}
	}

	res, err := c.send(req)
	if err != nil {
		// Unwrap any URL errors. We don't really need to expose the
		// underlying HTTP nonsense to the user.
		if urlerr, ok := err.(*url.Error); ok {
			err = urlerr.Err
		}

		if netoperr, ok := err.(*net.OpError); ok && netoperr.Op == "dial" {
			// Connection refused.
			if c.fallback != nil {
				// XXX: this runs the PreRun twice
				return c.fallback.Execute(req, re, env)
			}
			err = fmt.Errorf("cannot connect to the api. Is the daemon running? To run as a standalone CLI command remove the api file in `$IPFS_PATH/api`")
		}
		return err
	}

	if cmd.PostRun != nil {
		if typer, ok := re.(interface {
			Type() cmds.PostRunType
		}); ok && cmd.PostRun[typer.Type()] != nil {
			err := cmd.PostRun[typer.Type()](res, re)
			closeErr := re.CloseWithError(err)
			if closeErr == cmds.ErrClosingClosedEmitter {
				// ignore double close errors
				return nil
			}

			return closeErr
		}
	}

	return cmds.Copy(re, res)
}

func (c *client) toHTTPRequest(req *cmds.Request) (*http.Request, error) {
	query, err := getQuery(req)
	if err != nil {
		return nil, err
	}

	var fileReader *files.MultiFileReader
	var reader io.Reader // in case we have no body to send we need to provide
	// untyped nil to http.NewRequest

	if bodyArgs := req.BodyArgs(); bodyArgs != nil {
		// In the end, this wraps a file reader in a file reader.
		// However, such is life.
		fileReader = files.NewMultiFileReader(files.NewMapDirectory(map[string]files.Node{
			"stdin": files.NewReaderFile(bodyArgs),
		}), true)
		reader = fileReader
	} else if req.Files != nil {
		fileReader = files.NewMultiFileReader(req.Files, true)
		reader = fileReader
	}

	path := strings.Join(req.Path, "/")
	url := fmt.Sprintf(ApiUrlFormat, c.serverAddress, c.apiPrefix, path, query)

	httpReq, err := http.NewRequest("POST", url, reader)
	if err != nil {
		return nil, err
	}

	// TODO extract string consts?
	if fileReader != nil {
		httpReq.Header.Set(contentTypeHeader, "multipart/form-data; boundary="+fileReader.Boundary())
	} else {
		httpReq.Header.Set(contentTypeHeader, applicationOctetStream)
	}
	httpReq.Header.Set(uaHeader, c.ua)

	httpReq = httpReq.WithContext(req.Context)
	httpReq.Close = true

	return httpReq, nil
}

func (c *client) send(req *cmds.Request) (cmds.Response, error) {
	if req.Context == nil {
		log.Warnf("no context set in request")
		req.Context = context.Background()
	}

	// save user-provided encoding
	previousUserProvidedEncoding, found := req.Options[cmds.EncLong].(string)

	// override with json to send to server
	req.SetOption(cmds.EncLong, cmds.JSON)

	// stream channel output
	req.SetOption(cmds.ChanOpt, true)

	// build http request
	httpReq, err := c.toHTTPRequest(req)
	if err != nil {
		return nil, err
	}

	// send http request
	httpRes, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	// parse using the overridden JSON encoding in request
	res, err := parseResponse(httpRes, req)
	if err != nil {
		return nil, err
	}

	// reset request encoding to what it was before
	if found && len(previousUserProvidedEncoding) > 0 {
		// reset to user provided encoding after sending request
		// NB: if user has provided an encoding but it is the empty string,
		// still leave it as JSON.
		req.SetOption(cmds.EncLong, previousUserProvidedEncoding)
	}

	return res, nil
}

func getQuery(req *cmds.Request) (string, error) {
	query := url.Values{}

	for k, v := range req.Options {
		if OptionSkipMap[k] {
			continue
		}
		str := fmt.Sprintf("%v", v)
		query.Set(k, str)
	}

	args := req.Arguments
	argDefs := req.Command.Arguments

	argDefIndex := 0

	for _, arg := range args {
		argDef := argDefs[argDefIndex]
		// skip ArgFiles
		for argDef.Type == cmds.ArgFile {
			argDefIndex++
			argDef = argDefs[argDefIndex]
		}

		query.Add("arg", arg)

		if len(argDefs) > argDefIndex+1 {
			argDefIndex++
		}
	}

	return query.Encode(), nil
}
