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
	"strconv"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/containerd/log"
	"github.com/docker/containerd/reference"
	"github.com/docker/containerd/remotes"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"golang.org/x/net/context/ctxhttp"
)

const (
	MediaTypeDockerSchema2Manifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerSchema2ManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// TODO(stevvooe): Create "multi-fetch" mode that just takes a remote
// then receives object/hint lines on stdin, returning content as
// needed.

var fetchObjectCommand = cli.Command{
	Name:        "fetch-object",
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
			ctx     = background
			timeout = context.Duration("timeout")
			ref     = context.Args().First()
		)

		if timeout > 0 {
			var cancel func()
			ctx, cancel = contextpkg.WithTimeout(ctx, timeout)
			defer cancel()
		}

		resolver, err := getResolver(ctx)
		if err != nil {
			return err
		}

		ctx = log.WithLogger(ctx, log.G(ctx).WithField("ref", ref))

		log.G(ctx).Infof("resolving")
		_, desc, fetcher, err := resolver.Resolve(ctx, ref)
		if err != nil {
			return err
		}

		log.G(ctx).Infof("fetching")
		rc, err := fetcher.Fetch(ctx, desc)
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

// getResolver prepares the resolver from the environment and options.
func getResolver(ctx contextpkg.Context) (remotes.Resolver, error) {
	return newDockerResolver(), nil
}

// NOTE(stevvooe): Most of the code below this point is prototype code to
// demonstrate a very simplified docker.io fetcher. We have a lot of hard coded
// values but we leave many of the details down to the fetcher, creating a lot
// of room for ways to fetch content.

type dockerFetcher struct {
	base  url.URL
	token string
}

func (r *dockerFetcher) url(ps ...string) string {
	url := r.base
	url.Path = path.Join(url.Path, path.Join(ps...))
	return url.String()
}

func (r *dockerFetcher) Fetch(ctx contextpkg.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	ctx = log.WithLogger(ctx, log.G(ctx).WithFields(
		logrus.Fields{
			"base":   r.base.String(),
			"digest": desc.Digest,
		},
	))

	paths, err := getV2URLPaths(desc)
	if err != nil {
		return nil, err
	}

	for _, path := range paths {
		u := r.url(path)

		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Accept", strings.Join([]string{desc.MediaType, `*`}, ", "))
		resp, err := r.doRequest(ctx, req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode > 299 {
			if resp.StatusCode == http.StatusNotFound {
				continue // try one of the other urls.
			}
			resp.Body.Close()
			return nil, errors.Errorf("unexpected status code %v: %v", u, resp.Status)
		}

		return resp.Body, nil
	}

	return nil, errors.New("not found")
}

func (r *dockerFetcher) doRequest(ctx contextpkg.Context, req *http.Request) (*http.Response, error) {
	ctx = log.WithLogger(ctx, log.G(ctx).WithField("url", req.URL.String()))
	log.G(ctx).WithField("request.headers", req.Header).Debug("fetch content")
	if r.token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.token))
	}

	resp, err := ctxhttp.Do(ctx, http.DefaultClient, req)
	if err != nil {
		return nil, err
	}
	log.G(ctx).WithFields(logrus.Fields{
		"status":           resp.Status,
		"response.headers": resp.Header,
	}).Debug("fetch response received")

	return resp, err
}

type dockerResolver struct{}

var _ remotes.Resolver = &dockerResolver{}

func newDockerResolver() remotes.Resolver {
	return &dockerResolver{}
}

func (r *dockerResolver) Resolve(ctx contextpkg.Context, ref string) (string, ocispec.Descriptor, remotes.Fetcher, error) {
	refspec, err := reference.Parse(ref)
	if err != nil {
		return "", ocispec.Descriptor{}, nil, err
	}

	var (
		base  url.URL
		token string
	)

	switch refspec.Hostname() {
	case "docker.io":
		base.Scheme = "https"
		base.Host = "registry-1.docker.io"
		prefix := strings.TrimPrefix(refspec.Locator, "docker.io/")
		base.Path = path.Join("/v2", prefix)
		token, err = getToken(ctx, "repository:"+prefix+":pull")
		if err != nil {
			return "", ocispec.Descriptor{}, nil, err
		}
	case "localhost:5000":
		base.Scheme = "http"
		base.Host = "localhost:5000"
		base.Path = path.Join("/v2", strings.TrimPrefix(refspec.Locator, "localhost:5000/"))
	default:
		return "", ocispec.Descriptor{}, nil, errors.Errorf("unsupported locator: %q", refspec.Locator)
	}

	fetcher := &dockerFetcher{
		base:  base,
		token: token,
	}

	var (
		urls []string
		dgst = refspec.Digest()
	)

	if dgst != "" {
		if err := dgst.Validate(); err != nil {
			// need to fail here, since we can't actually resolve the invalid
			// digest.
			return "", ocispec.Descriptor{}, nil, err
		}

		// turns out, we have a valid digest, make a url.
		urls = append(urls, fetcher.url("manifests", dgst.String()))
	} else {
		urls = append(urls, fetcher.url("manifests", refspec.Object))
	}

	// fallback to blobs on not found.
	urls = append(urls, fetcher.url("blobs", dgst.String()))

	for _, u := range urls {
		req, err := http.NewRequest(http.MethodHead, u, nil)
		if err != nil {
			return "", ocispec.Descriptor{}, nil, err
		}

		// set headers for all the types we support for resolution.
		req.Header.Set("Accept", strings.Join([]string{
			MediaTypeDockerSchema2Manifest,
			MediaTypeDockerSchema2ManifestList,
			ocispec.MediaTypeImageManifest,
			ocispec.MediaTypeImageIndex, "*"}, ", "))

		log.G(ctx).Debug("resolving")
		resp, err := fetcher.doRequest(ctx, req)
		if err != nil {
			return "", ocispec.Descriptor{}, nil, err
		}
		resp.Body.Close() // don't care about body contents.

		if resp.StatusCode > 299 {
			if resp.StatusCode == http.StatusNotFound {
				continue
			}
			return "", ocispec.Descriptor{}, nil, errors.Errorf("unexpected status code %v: %v", u, resp.Status)
		}

		// this is the only point at which we trust the registry. we use the
		// content headers to assemble a descriptor for the name. when this becomes
		// more robust, we mostly get this information from a secure trust store.
		dgstHeader := digest.Digest(resp.Header.Get("Docker-Content-Digest"))

		if dgstHeader != "" {
			if err := dgstHeader.Validate(); err != nil {
				if err == nil {
					return "", ocispec.Descriptor{}, nil, errors.Errorf("%q in header not a valid digest", dgstHeader)
				}
				return "", ocispec.Descriptor{}, nil, errors.Wrapf(err, "%q in header not a valid digest", dgstHeader)
			}
			dgst = dgstHeader
		}

		if dgst == "" {
			return "", ocispec.Descriptor{}, nil, errors.Wrapf(err, "could not resolve digest for %v", ref)
		}

		var (
			size       int64
			sizeHeader = resp.Header.Get("Content-Length")
		)

		size, err = strconv.ParseInt(sizeHeader, 10, 64)
		if err != nil || size < 0 {
			return "", ocispec.Descriptor{}, nil, errors.Wrapf(err, "%q in header not a valid size", sizeHeader)
		}

		desc := ocispec.Descriptor{
			Digest:    dgst,
			MediaType: resp.Header.Get("Content-Type"), // need to strip disposition?
			Size:      size,
		}

		log.G(ctx).WithField("desc.digest", desc.Digest).Debug("resolved")
		return ref, desc, fetcher, nil
	}

	return "", ocispec.Descriptor{}, nil, errors.Errorf("%v not found", ref)
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
func getV2URLPaths(desc ocispec.Descriptor) ([]string, error) {
	var urls []string

	switch desc.MediaType {
	case MediaTypeDockerSchema2Manifest, MediaTypeDockerSchema2ManifestList,
		ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:
		urls = append(urls, path.Join("manifests", desc.Digest.String()))
	}

	// always fallback to attempting to get the object out of the blobs store.
	urls = append(urls, path.Join("blobs", desc.Digest.String()))

	return urls, nil
}
