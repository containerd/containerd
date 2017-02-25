package main

import (
	contextpkg "context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/remotes"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/net/context/ctxhttp"
)

// TODO(stevvooe): Create "multi-fetch" mode that just takes a remote
// then receives object/hint lines on stdin, returning content as
// needed.

var fetchCommand = cli.Command{
	Name:        "fetch",
	Usage:       "retrieve objects from a remote",
	ArgsUsage:   "[flags] <remote> <object> [<hint>, ...]",
	Description: `Fetch objects by identifier from a remote.`,
	Flags: []cli.Flag{
		cli.DurationFlag{
			Name:   "timeout",
			Usage:  "total timeout for fetch",
			EnvVar: "CONTAINERD_FETCH_TIMEOUT",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			ctx     = contextpkg.Background()
			timeout = context.Duration("timeout")
			locator = context.Args().First()
			args    = context.Args().Tail()
		)

		if timeout > 0 {
			var cancel func()
			ctx, cancel = contextpkg.WithTimeout(ctx, timeout)
			defer cancel()
		}

		if locator == "" {
			return errors.New("containerd: remote required")
		}

		if len(args) < 1 {
			return errors.New("containerd: object required")
		}

		object := args[0]
		hints := args[1:]

		resolver, err := getResolver(ctx)
		if err != nil {
			return err
		}

		remote, err := resolver.Resolve(ctx, locator)
		if err != nil {
			return err
		}

		ctx = log.WithLogger(ctx, log.G(ctx).WithFields(
			logrus.Fields{
				"remote": locator,
				"object": object,
			}))

		log.G(ctx).Infof("fetching")
		rc, err := remote.Fetch(ctx, object, hints...)
		if err != nil {
			return err
		}
		defer rc.Close()

		if _, err := io.Copy(os.Stdout, rc); err != nil {
			return err
		}

		return nil
	},
}

// NOTE(stevvooe): Most of the code below this point is prototype code to
// demonstrate a very simplified docker.io fetcher. We have a lot of hard coded
// values but we leave many of the details down to the fetcher, creating a lot
// of room for ways to fetch content.

// getResolver prepares the resolver from the environment and options.
func getResolver(ctx contextpkg.Context) (remotes.Resolver, error) {
	return remotes.ResolverFunc(func(ctx contextpkg.Context, locator string) (remotes.Fetcher, error) {
		if !strings.HasPrefix(locator, "docker.io") && !strings.HasPrefix(locator, "localhost:5000") {
			return nil, errors.Errorf("unsupported locator: %q", locator)
		}

		var (
			base = url.URL{
				Scheme: "https",
				Host:   "registry-1.docker.io",
			}
			prefix = strings.TrimPrefix(locator, "docker.io/")
		)

		if strings.HasPrefix(locator, "localhost:5000") {
			base.Scheme = "http"
			base.Host = "localhost:5000"
			prefix = strings.TrimPrefix(locator, "localhost:5000/")
		}

		token, err := getToken(ctx, "repository:"+prefix+":pull")
		if err != nil {
			return nil, err
		}

		return remotes.FetcherFunc(func(ctx contextpkg.Context, object string, hints ...string) (io.ReadCloser, error) {
			ctx = log.WithLogger(ctx, log.G(ctx).WithFields(
				logrus.Fields{
					"prefix": prefix, // or repo?
					"base":   base.String(),
					"hints":  hints,
				},
			))

			paths, err := getV2URLPaths(prefix, object, hints...)
			if err != nil {
				return nil, err
			}

			for _, path := range paths {
				url := base
				url.Path = path
				log.G(ctx).WithField("url", url).Debug("fetch content")

				req, err := http.NewRequest(http.MethodGet, url.String(), nil)
				if err != nil {
					return nil, err
				}

				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
				for _, mediatype := range remotes.HintValues("mediatype", hints...) {
					req.Header.Set("Accept", mediatype)
				}

				resp, err := ctxhttp.Do(ctx, http.DefaultClient, req)
				if err != nil {
					return nil, err
				}

				if resp.StatusCode > 299 {
					if resp.StatusCode == http.StatusNotFound {
						continue // try one of the other urls.
					}
					resp.Body.Close()
					return nil, errors.Errorf("unexpected status code %v: %v", url, resp.Status)
				}

				return resp.Body, nil
			}

			return nil, errors.New("not found")
		}), nil
	}), nil
}

func getToken(ctx contextpkg.Context, scopes ...string) (string, error) {
	var (
		u = url.URL{
			Scheme: "https",
			Host:   "auth.docker.io",
			Path:   "/token",
		}

		q = url.Values{
			"scope":   scopes,
			"service": []string{"registry.docker.io"}, // usually comes from auth challenge
		}
	)

	u.RawQuery = q.Encode()

	log.G(ctx).WithField("token.url", u.String()).Debug("requesting token")
	resp, err := ctxhttp.Get(ctx, http.DefaultClient, u.String())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return "", errors.Errorf("unexpected status code: %v %v", resp.StatusCode, resp.Status)
	}

	p, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tokenResponse struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(p, &tokenResponse); err != nil {
		return "", err
	}

	return tokenResponse.Token, nil
}

// getV2URLPaths generates the candidate urls paths for the object based on the
// set of hints and the provided object id. URLs are returned in the order of
// most to least likely succeed.
func getV2URLPaths(prefix, object string, hints ...string) ([]string, error) {
	var urls []string

	// TODO(stevvooe): We can probably define a higher-level "type" hint to
	// avoid having to do extra round trips to resolve content, as well as
	// avoid the tedium of providing media types.

	if remotes.HintExists("mediatype", "application/vnd.docker.distribution.manifest.v2+json", hints...) { // TODO(stevvooe): make this handle oci types, as well.
		// fast path out if we know we are getting a manifest. Arguably, we
		// should fallback to blobs, just in case.
		urls = append(urls, path.Join("/v2", prefix, "manifests", object))
	}

	_, err := digest.Parse(object)
	if err == nil {
		// we have a digest, use blob or manifest path, depending on hints, may
		// need to try both.
		urls = append(urls, path.Join("/v2", prefix, "blobs", object))
	}

	// probably a take, so we go through the manifests endpoint
	urls = append(urls, path.Join("/v2", prefix, "manifests", object))

	var (
		noduplicates []string
		seen         = map[string]struct{}{}
	)
	for _, u := range urls {
		if _, ok := seen[u]; !ok {
			seen[u] = struct{}{}
			noduplicates = append(noduplicates, u)
		}
	}

	return noduplicates, nil
}
