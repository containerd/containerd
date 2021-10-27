package http

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	cmds "github.com/ipfs/go-ipfs-cmds"
	logging "github.com/ipfs/go-log"
	cors "github.com/rs/cors"
)

var log = logging.Logger("cmds/http")

var (
	// ErrNotFound is returned when the endpoint does not exist.
	ErrNotFound = errors.New("404 page not found")
)

const (
	// StreamErrHeader is used as trailer when stream errors happen.
	StreamErrHeader          = "X-Stream-Error"
	streamHeader             = "X-Stream-Output"
	channelHeader            = "X-Chunked-Output"
	extraContentLengthHeader = "X-Content-Length"
	uaHeader                 = "User-Agent"
	contentTypeHeader        = "Content-Type"
	contentDispHeader        = "Content-Disposition"
	transferEncodingHeader   = "Transfer-Encoding"
	originHeader             = "origin"

	applicationJSON        = "application/json"
	applicationOctetStream = "application/octet-stream"
	plainText              = "text/plain"
)

func skipAPIHeader(h string) bool {
	switch h {
	case "Access-Control-Allow-Origin":
		return true
	case "Access-Control-Allow-Methods":
		return true
	case "Access-Control-Allow-Credentials":
		return true
	default:
		return false
	}
}

// the internal handler for the API
type handler struct {
	root *cmds.Command
	cfg  *ServerConfig
	env  cmds.Environment
}

// NewHandler creates the http.Handler for the given commands.
func NewHandler(env cmds.Environment, root *cmds.Command, cfg *ServerConfig) http.Handler {
	if cfg == nil {
		panic("must provide a valid ServerConfig")
	}

	c := cors.New(*cfg.corsOpts)

	var h http.Handler

	h = &handler{
		env:  env,
		root: root,
		cfg:  cfg,
	}

	if cfg.APIPath != "" {
		h = newPrefixHandler(cfg.APIPath, h) // wrap with path prefix checker and trimmer
	}
	h = c.Handler(h) // wrap with CORS handler

	return h
}

type requestLogger interface {
	LogRequest(*cmds.Request) func()
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Debug("incoming API request: ", r.URL)

	defer func() {
		if r := recover(); r != nil {
			log.Error("a panic has occurred in the commands handler!")
			log.Error(r)
			log.Errorf("stack trace:\n%s", debug.Stack())
		}
	}()

	// First of all, check if we are allowed to handle the request method
	// or we are configured not to.
	//
	// Always allow OPTIONS, POST
	switch r.Method {
	case http.MethodOptions:
		// If we get here, this is a normal (non-preflight) request.
		// The CORS library handles all other requests.

		// Tell the user the allowed methods, and return.
		setAllowHeader(w, h.cfg.AllowGet)
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	case http.MethodGet, http.MethodHead:
		if h.cfg.AllowGet {
			break
		}
		fallthrough
	default:
		setAllowHeader(w, h.cfg.AllowGet)
		http.Error(w, "405 - Method Not Allowed", http.StatusMethodNotAllowed)
		log.Warnf("The IPFS API does not support %s requests.", r.Method)
		return
	}

	if !allowOrigin(r, h.cfg) || !allowReferer(r, h.cfg) || !allowUserAgent(r, h.cfg) {
		http.Error(w, "403 - Forbidden", http.StatusForbidden)
		log.Warnf("API blocked request to %s. (possible CSRF)", r.URL)
		return
	}

	// If we have a request body, make sure the preamble
	// knows that it should close the body if it wants to
	// write before completing reading.
	// FIXME: https://github.com/ipfs/go-ipfs/issues/5168
	// FIXME: https://github.com/golang/go/issues/15527
	var bodyEOFChan chan struct{}
	if r.Body != http.NoBody {
		bodyEOFChan = make(chan struct{})
		var once sync.Once
		bw := bodyWrapper{
			ReadCloser: r.Body,
			onEOF: func() {
				once.Do(func() { close(bodyEOFChan) })
			},
		}
		r.Body = bw
	}

	req, err := parseRequest(r, h.root)
	if err != nil {
		status := http.StatusBadRequest
		if err == ErrNotFound {
			status = http.StatusNotFound
		}

		http.Error(w, err.Error(), status)
		return
	}

	// set user's headers first.
	for k, v := range h.cfg.Headers {
		if !skipAPIHeader(k) {
			w.Header()[k] = v
		}
	}

	// Handle the timeout up front.
	var cancel func()
	if timeoutStr, ok := req.Options[cmds.TimeoutOpt]; ok {
		timeout, err := time.ParseDuration(timeoutStr.(string))
		if err != nil {
			return
		}
		req.Context, cancel = context.WithTimeout(req.Context, timeout)
	} else {
		req.Context, cancel = context.WithCancel(req.Context)
	}
	defer cancel()

	re, err := NewResponseEmitter(w, r.Method, req, withRequestBodyEOFChan(bodyEOFChan))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if reqLogger, ok := h.env.(requestLogger); ok {
		done := reqLogger.LogRequest(req)
		defer done()
	}

	h.root.Call(req, re, h.env)
}

func setAllowHeader(w http.ResponseWriter, allowGet bool) {
	allowedMethods := []string{http.MethodOptions, http.MethodPost}
	if allowGet {
		allowedMethods = append(allowedMethods, http.MethodHead, http.MethodGet)
	}
	w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
}
