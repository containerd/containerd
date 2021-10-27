package http

import (
	"net/http"
	"net/url"
	"strings"
	"sync"

	cors "github.com/rs/cors"
)

const (
	ACAOrigin      = "Access-Control-Allow-Origin"
	ACAMethods     = "Access-Control-Allow-Methods"
	ACACredentials = "Access-Control-Allow-Credentials"
)

type ServerConfig struct {
	// APIPath is the prefix of all request paths.
	// Example: host:port/api/v0/add. Here the APIPath is /api/v0
	APIPath string

	// Headers is an optional map of headers that is written out.
	Headers map[string][]string

	// AllowGet indicates whether or not this server accepts GET requests.
	// When unset, the server only accepts POST, HEAD, and OPTIONS.
	//
	// This is different from CORS AllowedMethods. The API may allow GET
	// requests in general, but reject them in CORS. That will allow
	// websites to include resources from the API but not _read_ them.
	AllowGet bool

	// corsOpts is a set of options for CORS headers.
	corsOpts *cors.Options

	// corsOptsRWMutex is a RWMutex for read/write CORSOpts
	corsOptsRWMutex sync.RWMutex
}

func NewServerConfig() *ServerConfig {
	cfg := new(ServerConfig)
	cfg.corsOpts = new(cors.Options)
	return cfg
}

func (cfg *ServerConfig) AllowedOrigins() []string {
	cfg.corsOptsRWMutex.RLock()
	defer cfg.corsOptsRWMutex.RUnlock()
	return cfg.corsOpts.AllowedOrigins
}

func (cfg *ServerConfig) SetAllowedOrigins(origins ...string) {
	cfg.corsOptsRWMutex.Lock()
	defer cfg.corsOptsRWMutex.Unlock()
	o := make([]string, len(origins))
	copy(o, origins)
	cfg.corsOpts.AllowedOrigins = o
}

func (cfg *ServerConfig) AppendAllowedOrigins(origins ...string) {
	cfg.corsOptsRWMutex.Lock()
	defer cfg.corsOptsRWMutex.Unlock()
	cfg.corsOpts.AllowedOrigins = append(cfg.corsOpts.AllowedOrigins, origins...)
}

func (cfg *ServerConfig) AllowedMethods() []string {
	cfg.corsOptsRWMutex.RLock()
	defer cfg.corsOptsRWMutex.RUnlock()
	return []string(cfg.corsOpts.AllowedMethods)
}

func (cfg *ServerConfig) SetAllowedMethods(methods ...string) {
	cfg.corsOptsRWMutex.Lock()
	defer cfg.corsOptsRWMutex.Unlock()
	if cfg.corsOpts == nil {
		cfg.corsOpts = new(cors.Options)
	}
	cfg.corsOpts.AllowedMethods = methods
}

func (cfg *ServerConfig) SetAllowCredentials(flag bool) {
	cfg.corsOptsRWMutex.Lock()
	defer cfg.corsOptsRWMutex.Unlock()
	cfg.corsOpts.AllowCredentials = flag
}

// allowOrigin just stops the request if the origin is not allowed.
// the CORS middleware apparently does not do this for us...
func allowOrigin(r *http.Request, cfg *ServerConfig) bool {
	origin := r.Header.Get("Origin")

	// curl, or ipfs shell, typing it in manually, or clicking link
	// NOT in a browser. this opens up a hole. we should close it,
	// but right now it would break things. TODO
	if origin == "" {
		return true
	}
	origins := cfg.AllowedOrigins()
	for _, o := range origins {
		if o == "*" { // ok! you asked for it!
			return true
		}

		if o == origin { // allowed explicitly
			return true
		}
	}

	return false
}

// allowReferer this is here to prevent some CSRF attacks that
// the API would be vulnerable to. We check that the Referer
// is allowed by CORS Origin (origins and referrers here will
// work similarly in the normal uses of the API).
// See discussion at https://github.com/ipfs/go-ipfs/issues/1532
func allowReferer(r *http.Request, cfg *ServerConfig) bool {
	referer := r.Referer()

	// curl, or ipfs shell, typing it in manually, or clicking link
	// NOT in a browser. this opens up a hole. we should close it,
	// but right now it would break things. TODO
	if referer == "" {
		return true
	}

	u, err := url.Parse(referer)
	if err != nil {
		// bad referer. but there _is_ something, so bail.
		// debug because referer comes straight from the client. dont want to
		// let people DOS by putting a huge referer that gets stored in log files.
		return false
	}
	origin := u.Scheme + "://" + u.Host

	// check CORS ACAOs and pretend Referer works like an origin.
	// this is valid for many (most?) sane uses of the API in
	// other applications, and will have the desired effect.
	origins := cfg.AllowedOrigins()
	for _, o := range origins {
		if o == "*" { // ok! you asked for it!
			return true
		}

		// referer is allowed explicitly
		if o == origin {
			return true
		}
	}

	return false
}

// allowUserAgent checks the request's user-agent against the list
// of DisallowUserAgents for requests with no origin nor referer set.
func allowUserAgent(r *http.Request, cfg *ServerConfig) bool {
	// This check affects POST as we should never get POST requests from a
	// browser without Origin or Referer, but we might:
	// https://bugzilla.mozilla.org/show_bug.cgi?id=429594.
	if r.Method != http.MethodPost {
		return true
	}

	origin := r.Header.Get("Origin")
	referer := r.Referer()

	// If these are set, we leave up to CORS and CSRF checks.
	if origin != "" || referer != "" {
		return true
	}

	// Allow if the user agent does not start with Mozilla... (i.e. curl).
	// Disallow otherwise.
	//
	// This means the request probably came from a browser and thus, it
	// should have included Origin or referer headers.
	ua := r.Header.Get("User-agent")
	return !strings.HasPrefix(ua, "Mozilla")
}
