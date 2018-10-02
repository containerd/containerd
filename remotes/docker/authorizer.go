/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context/ctxhttp"
)

type hostAuth struct {
	c *sync.Cond

	requests  map[string]struct{}
	responses int

	f func(context.Context) (string, error)
}

type dockerAuthorizer struct {
	credentials func(string) (string, string, error)

	client *http.Client
	mu     sync.Mutex

	auth map[string]*hostAuth
}

// NewAuthorizer creates a Docker authorizer using the provided function to
// get credentials for the token server or basic auth.
func NewAuthorizer(client *http.Client, f func(string) (string, string, error)) Authorizer {
	if client == nil {
		client = http.DefaultClient
	}
	return &dockerAuthorizer{
		credentials: f,
		client:      client,
		auth:        map[string]*hostAuth{},
	}
}
func (auth *hostAuth) registerID(ctx context.Context, id string) {
	auth.requests[id] = struct{}{}

	// Clean up request after cancelled
	go func() {
		<-ctx.Done()

		auth.c.L.Lock()
		defer auth.c.L.Unlock()

		if _, ok := auth.requests[id]; ok {
			delete(auth.requests, id)
			auth.c.Signal()
		}
	}()
}
func (a *dockerAuthorizer) Authorize(ctx context.Context, req *http.Request) error {
	auth := a.getAuth(req.URL.Host)
	id := getRequestID(ctx)

	auth.c.L.Lock()
	defer auth.c.L.Unlock()

	if auth.requests == nil {
		// first request, need a response before authorizing
		auth.requests = map[string]struct{}{}
		if id != "" {
			auth.registerID(ctx, id)
		}
		return nil
	}

	if auth.responses == 0 {
		// wait for a response
		auth.c.Wait()
	}

	if id != "" {
		// wait for number of requests to decrease
		for len(auth.requests) >= 5 {
			auth.c.Wait()
		}

		auth.registerID(ctx, id)
	}

	if auth.f != nil {
		h, err := auth.f(ctx)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", h)
	}

	return nil
}

func (a *dockerAuthorizer) AddResponses(ctx context.Context, responses []*http.Response) error {
	last := responses[len(responses)-1]
	host := last.Request.URL.Host

	auth := a.getAuth(host)

	auth.c.L.Lock()
	defer func() {

		id := getRequestID(ctx)
		if id != "" {
			delete(auth.requests, id)
		}
		auth.responses++
		if auth.responses == 1 {
			// First response, broadcast to wake all requests
			// waiting for initial auth response
			auth.c.Broadcast()
		} else {
			auth.c.Signal()
		}
		auth.c.L.Unlock()
	}()

	if len(responses) > 5 {
		return errors.Wrap(ErrTooManyResponses, "exceeded 5 responses")
	}

	if last.StatusCode == http.StatusUnauthorized {
		log.G(ctx).WithField("header", last.Header.Get("WWW-Authenticate")).Debug("Unauthorized")

		for _, c := range parseAuthHeader(last.Header) {
			if c.scheme == bearerAuth {
				if err := invalidAuthorization(c, responses); err != nil {
					auth.f = nil
					return err
				}

				f, err := a.tokenAuth(ctx, host, c.parameters)
				if err != nil {
					return err
				}
				auth.f = f

				return ErrRetryWithAuthorization
			} else if c.scheme == basicAuth {
				// TODO: Resolve credentials on authorize
				username, secret, err := a.credentials(host)
				if err != nil {
					return err
				}
				if username != "" && secret != "" {
					auth.f = func(context.Context) (string, error) {
						auth := username + ":" + secret
						return fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(auth))), nil
					}

					return ErrRetryWithAuthorization
				}
			}
		}
	}

	return nil
}

func (a *dockerAuthorizer) getAuth(host string) *hostAuth {
	a.mu.Lock()
	defer a.mu.Unlock()

	auth := a.auth[host]
	if auth == nil {
		auth = &hostAuth{
			c: sync.NewCond(&sync.Mutex{}),
		}
		a.auth[host] = auth
	}

	return auth
}

func (a *dockerAuthorizer) tokenAuth(ctx context.Context, host string, params map[string]string) (func(context.Context) (string, error), error) {
	realm, ok := params["realm"]
	if !ok {
		return nil, errors.New("no realm specified for token auth challenge")
	}

	realmURL, err := url.Parse(realm)
	if err != nil {
		return nil, errors.Wrap(err, "invalid token auth challenge realm")
	}

	common := tokenOptions{
		realm:   realmURL.String(),
		service: params["service"],
	}

	if a.credentials != nil {
		common.username, common.secret, err = a.credentials(host)
		if err != nil {
			return nil, err
		}
	}

	scopedTokens := map[string]string{}

	return func(ctx context.Context) (string, error) {
		// copy common token options
		to := common

		to.scopes = getTokenScopes(ctx, params["scope"])
		if len(to.scopes) == 0 {
			return "", errors.Errorf("no scope specified for token auth challenge")
		}

		tokenKey := strings.Join(to.scopes, " ")
		token := scopedTokens[tokenKey]
		if token == "" {
			if to.secret != "" {
				// Credential information is provided, use oauth POST endpoint
				token, err = a.fetchTokenWithOAuth(ctx, to)
				if err != nil {
					return "", errors.Wrap(err, "failed to fetch oauth token")
				}
			} else {
				// Do request anonymously
				token, err = a.fetchToken(ctx, to)
				if err != nil {
					return "", errors.Wrap(err, "failed to fetch anonymous token")
				}
			}
			scopedTokens[tokenKey] = token
		}
		return fmt.Sprintf("Bearer %s", token), nil
	}, nil
}

type tokenOptions struct {
	realm    string
	service  string
	scopes   []string
	username string
	secret   string
}

type postTokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	IssuedAt     time.Time `json:"issued_at"`
	Scope        string    `json:"scope"`
}

func (a *dockerAuthorizer) fetchTokenWithOAuth(ctx context.Context, to tokenOptions) (string, error) {
	form := url.Values{}
	form.Set("scope", strings.Join(to.scopes, " "))
	form.Set("service", to.service)
	// TODO: Allow setting client_id
	form.Set("client_id", "containerd-client")

	if to.username == "" {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", to.secret)
	} else {
		form.Set("grant_type", "password")
		form.Set("username", to.username)
		form.Set("password", to.secret)
	}

	resp, err := ctxhttp.PostForm(ctx, a.client, to.realm, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Registries without support for POST may return 404 for POST /v2/token.
	// As of September 2017, GCR is known to return 404.
	// As of February 2018, JFrog Artifactory is known to return 401.
	if (resp.StatusCode == 405 && to.username != "") || resp.StatusCode == 404 || resp.StatusCode == 401 {
		return a.fetchToken(ctx, to)
	} else if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		b, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 64000)) // 64KB
		log.G(ctx).WithFields(logrus.Fields{
			"status": resp.Status,
			"body":   string(b),
		}).Debugf("token request failed")
		// TODO: handle error body and write debug output
		return "", errors.Errorf("unexpected status: %s", resp.Status)
	}

	decoder := json.NewDecoder(resp.Body)

	var tr postTokenResponse
	if err = decoder.Decode(&tr); err != nil {
		return "", fmt.Errorf("unable to decode token response: %s", err)
	}

	return tr.AccessToken, nil
}

type getTokenResponse struct {
	Token        string    `json:"token"`
	AccessToken  string    `json:"access_token"`
	ExpiresIn    int       `json:"expires_in"`
	IssuedAt     time.Time `json:"issued_at"`
	RefreshToken string    `json:"refresh_token"`
}

// getToken fetches a token using a GET request
func (a *dockerAuthorizer) fetchToken(ctx context.Context, to tokenOptions) (string, error) {
	req, err := http.NewRequest("GET", to.realm, nil)
	if err != nil {
		return "", err
	}

	reqParams := req.URL.Query()

	if to.service != "" {
		reqParams.Add("service", to.service)
	}

	for _, scope := range to.scopes {
		reqParams.Add("scope", scope)
	}

	if to.secret != "" {
		req.SetBasicAuth(to.username, to.secret)
	}

	req.URL.RawQuery = reqParams.Encode()

	resp, err := ctxhttp.Do(ctx, a.client, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		// TODO: handle error body and write debug output
		return "", errors.Errorf("unexpected status: %s", resp.Status)
	}

	decoder := json.NewDecoder(resp.Body)

	var tr getTokenResponse
	if err = decoder.Decode(&tr); err != nil {
		return "", fmt.Errorf("unable to decode token response: %s", err)
	}

	// `access_token` is equivalent to `token` and if both are specified
	// the choice is undefined.  Canonicalize `access_token` by sticking
	// things in `token`.
	if tr.AccessToken != "" {
		tr.Token = tr.AccessToken
	}

	if tr.Token == "" {
		return "", ErrNoToken
	}

	return tr.Token, nil
}

func invalidAuthorization(c challenge, responses []*http.Response) error {
	errStr := c.parameters["error"]
	if errStr == "" {
		return nil
	}

	n := len(responses)
	if n == 1 || (n > 1 && !sameRequest(responses[n-2].Request, responses[n-1].Request)) {
		return nil
	}

	return errors.Wrapf(ErrInvalidAuthorization, "server message: %s", errStr)
}

func sameRequest(r1, r2 *http.Request) bool {
	if r1.Method != r2.Method {
		return false
	}
	if *r1.URL != *r2.URL {
		return false
	}
	return true
}
